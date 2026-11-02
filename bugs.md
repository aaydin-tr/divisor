# Bugs — post-1.0

Defects found in the post-1.0 review (2026-10-29), against `v1.0.0`. Missing
capabilities are in [features.md](features.md); the 1.0 record and its
carry-over items are in [TODO.md](TODO.md).

Each entry says how it was verified:

- **reproduced**: a throwaway test against a copy of `v1.0.0` showed it; the
  numbers quoted are from that run
- **code-read**: traced through divisor's code and the library source in the
  module cache (fasthttp v1.77.0, gopsutil v4.26.9), not run

The **Regression test** line of each entry is the scenario that pins the fix.

## Checklist

High
- [ ] [B1 — ip-hash routes a client differently on each replica after a Reload](#b1)
- [ ] [B2 — least-response-time exiles a Backend after one failed request](#b2)
- [ ] [B3 — least-response-time sends every concurrent request to one Backend](#b3)
- [ ] [B4 — 502/504 responses carry the failed Backend's headers](#b4)

Medium
- [ ] [B5 — A non-idempotent request timing out mid-body gets 502, not 504](#b5)
- [ ] [B6 — w-round-robin cannot Reload between one and several Backends](#b6)
- [ ] [B7 — health_check_path without a leading slash keeps a Backend Down forever](#b7)
- [ ] [B8 — cert_file without key_file (or the reverse) serves plain HTTP](#b8)
- [ ] [B9 — Listeners share their port silently and cannot bind IPv6](#b9)

Low
- [ ] [B10 — process_cpu_percent is a lifetime average](#b10)
- [ ] [B11 — No read or idle timeout by default on the client listeners](#b11)
- [ ] [B12 — backends[].weight has no upper bound](#b12)
- [ ] [B13 — The monitoring dashboard needs the internet](#b13)
- [ ] [B14 — ip-hash keeps every Backend it ever Joined](#b14)

Docs
- [ ] [D1 — The Balancer guide recommends ip-hash and least-response-time without caveats](#d1)
- [ ] [D2 — Getting started understates the 502s before a Probe notices a Down Backend](#d2)
- [ ] [D3 — The benchmark is from 0.x](#d3)

---

## High

<a id="b1"></a>
### B1. ip-hash routes a client differently on each replica after a Reload

- **Verified:** reproduced
- **Where:** [core/ip-hash/ip_hash.go:50-52](core/ip-hash/ip_hash.go#L50-L52) (ring node keyed by Index), [pkg/consistent/consistent.go:94-96](pkg/consistent/consistent.go#L94-L96) (`Index|i|Addr`), [core/pool/pool.go:151](core/pool/pool.go#L151) (a Reload gives an added Backend a never-used Index), [core/balancer.go:58-61](core/balancer.go#L58-L61) (boot uses config positions)
- **What happens:** a Backend's Virtual nodes depend on its Index, and the
  Index depends on the process's Reload history, not on the config. Replica 1
  booted on `[A, B, C]` and Reloaded to `[A, C, D]` holds Indexes `[0 2 3]`;
  replica 2 booted on `[A, C, D]` holds `[0 1 2]`. With the same config,
  **619 of 1000 Client IPs were routed to a different Backend** by the two.
- **Impact:** the Helm chart and the example manifests run 2 replicas behind
  one Service, so a client alternating between pods loses affinity, the only
  reason to choose ip-hash. One replica alone reshuffles its clients on every
  restart after a Reload that removed or inserted a Backend.
- **Fix:** derive ring positions from the config alone: the Backend address
  plus its occurrence number among entries with that address (`addr#0`,
  `addr#1`, which keeps duplicate addresses apart, as M4 required). Keep the
  Index for stats and `/metrics` only. Ring positions move once, like M4/L21:
  note it in the release notes.
- **Regression test:** boot one Balancer on `[A, B, C]` and Reload it to
  `[A, C, D]`, boot another on `[A, C, D]`; every Client IP Picks the same
  address on both.

<a id="b2"></a>
### B2. least-response-time exiles a Backend after one failed request

- **Verified:** reproduced
- **Where:** [internal/proxy/proxy.go:223-225](internal/proxy/proxy.go#L223-L225) (a failure scores at least 10s), [core/least-response-time/least_response_time.go:17-37](core/least-response-time/least_response_time.go#L17-L37) (strict minimum), [core/pool/pool.go:209](core/pool/pool.go#L209) (the score resets only on Rejoin)
- **What happens:** one failed request lifts a Backend's moving average to
  seconds. Pick only ever chooses the lowest score, so the Backend is never
  picked again, never gets a new sample, and its score never comes down. It
  resets only on Rejoin, which never happens while its Probe keeps
  succeeding. Two Backends, the faster answering in ~0 ms with one request
  outliving `proxy_timeout`, the slower in 3 ms: of 1000 requests **the
  faster got 5, the slower 995**.
- **Impact:** one transient timeout or reset permanently removes healthy
  capacity until that Backend goes Down and Rejoins or divisor restarts.
  review.md H5 accepted this for a Backend that keeps failing while its
  Probe passes; it equally hits a single blip.
- **Fix:** H5 built and removed a staleness window as over-engineered;
  this case is the evidence for bringing time back in. Preferably fix it
  together with B3 through [features.md F5](features.md#f5) (power of two
  choices samples non-minimum Backends by construction). A narrow fix on its
  own: decay a score toward unmeasured once a Backend has gone without a
  sample for a few Probe intervals.
- **Regression test:** two Alive Backends, one failure on the faster one,
  then N sequential requests; the faster Backend is picked again within a
  bounded number of requests or time.

<a id="b3"></a>
### B3. least-response-time sends every concurrent request to one Backend

- **Verified:** reproduced at startup and for the burst after it; the
  steady-state case is code-read
- **Where:** [core/least-response-time/least_response_time.go:24-35](core/least-response-time/least_response_time.go#L24-L35)
- **What happens:** an unmeasured Backend (score 0: never answered, or just
  Rejoined) wins outright until its first response is scored, and every other
  pick takes the single lowest score with no load term. Two Backends, both
  50 ms: 100 concurrent requests at startup went **100 to `backend[0]`, 0 to
  `backend[1]`**; the next 100 went **0 and 100** (`backend[1]` was now the
  unmeasured one). In steady state, all requests in flight follow the current
  minimum until its moving average crosses another Backend's.
- **Impact:** under concurrency the Balancer piles a whole burst onto one
  Backend, the opposite of what `examples/README.md` promises ("Performance
  optimization"). A Backend that Rejoins after a crash takes every request
  arriving during its first round trip.
- **Fix:** [features.md F5](features.md#f5): power of two choices over
  `score × (pending + 1)`, with an unmeasured Backend scored as the pool's
  median rather than best.
- **Regression test:** two equal Backends, 100 concurrent requests from a
  cold start; each gets a share within a tolerance.

<a id="b4"></a>
### B4. 502/504 responses carry the failed Backend's headers

- **Verified:** reproduced
- **Where:** [internal/proxy/proxy.go:413-423](internal/proxy/proxy.go#L413-L423) (`serverError` sets status, body and `Content-Type` only), [internal/proxy/proxy.go:148](internal/proxy/proxy.go#L148) (the buffered path reads into `ctx.Response` directly); fasthttp resets a response only before each attempt
- **What happens:** with `server.streaming` off (the default), a Backend that
  sends its status and headers and then stalls or fails mid-body leaves those
  headers in the response divisor sends. A Backend sending
  `Cache-Control: public, max-age=3600`, `Set-Cookie: session=…` and
  `Content-Encoding: gzip` before stalling produced a `504` with all three
  headers and divisor's plain JSON body. With `server.streaming: true` the
  same scenario is clean: that path reads into a separate response
  ([internal/proxy/streaming.go:28-35](internal/proxy/streaming.go#L28-L35)).
- **Impact:** a CDN or browser may cache the 504 for an hour; clients fail to
  decode a "gzip" body that is not gzip; a Backend's cookie rides on an error
  page.
- **Fix:** reset the response after a failed attempt, before `OnResponse`
  runs, so both paths agree. Headers a Middleware adds in `OnResponse` still
  survive (they are written after the reset). Update the contract text that
  describes the leftover response: [docs/middleware.md:139-144](docs/middleware.md#L139-L144)
  ("may still hold whatever the Backend sent before failing") and the
  `Reset()` advice at [:219](docs/middleware.md#L219) and [:228-230](docs/middleware.md#L228-L230).
  ADR 0005's reasoning is unaffected.
- **Regression test:** a Backend that sends headers and then stalls past
  `proxy_timeout`; the 504 carries none of its headers, on both values of
  `server.streaming`.

## Medium

<a id="b5"></a>
### B5. A non-idempotent request timing out mid-body gets 502, not 504

- **Verified:** reproduced
- **Where:** [internal/proxy/proxy.go:178-183](internal/proxy/proxy.go#L178-L183); fasthttp maps a timeout to `ErrTimeout` only while reading headers (`header.go:2731-2733` in fasthttp v1.77.0)
- **What happens:** when the Backend's headers arrive and its body stalls past
  `proxy_timeout`, a GET is retried by fasthttp and ends as `ErrTimeout`
  (504), but a POST is not retried and returns the raw `i/o timeout`, which
  `serverError` answers with **502**. The streaming path already maps it
  ([internal/proxy/streaming.go:98-104](internal/proxy/streaming.go#L98-L104),
  `asGatewayTimeout`): with `server.streaming: true` the POST gets 504.
- **Impact:** the status contradicts the docs ("`504` when
  `server.proxy_timeout` expired") and depends on the method and on an
  unrelated knob.
- **Fix:** return `asGatewayTimeout(err)` from `doWithProxyTimeout`. Keep
  dial timeouts at 502 (the rule in `serverError`'s comment): check that
  fasthttp's `ErrDialTimeout` is not a `net.Error` timeout before relying on
  it.
- **Regression test:** the B4 Backend with POST; the client gets 504 on both
  values of `server.streaming`.

<a id="b6"></a>
### B6. w-round-robin cannot Reload between one and several Backends

- **Verified:** reproduced
- **Where:** [pkg/config/config.go:286-288](pkg/config/config.go#L286-L288), with `type` restart-only in [pkg/config/reload.go:27-31](pkg/config/reload.go#L27-L31)
- **What happens:** `PrepareConfig` rewrites `type: w-round-robin` to
  `round-robin` when there is one Backend. Scaling the file from one Backend
  to two (or two to one) while it says `w-round-robin` both times is rejected:
  `reload rejected, these keys only take effect on restart: type`.
- **Impact:** a Kubernetes scale-up from one Backend needs a restart, and
  the log blames a key the user never changed.
- **Fix:** stop rewriting `type`. w-round-robin works with one Backend; let a
  single Backend's missing `weight` default to 1. Update
  [docs/configuration.md:38](docs/configuration.md#L38) and
  [:197-198](docs/configuration.md#L197-L198) ("runs as `round-robin`").
- **Regression test:** `PlanReload` from a one-Backend w-round-robin config to
  a two-Backend one is accepted, and the reverse.

<a id="b7"></a>
### B7. health_check_path without a leading slash keeps a Backend Down forever

- **Verified:** reproduced
- **Where:** [pkg/config/config.go:138-140](pkg/config/config.go#L138-L140), [:350-352](pkg/config/config.go#L350-L352)
- **What happens:** `health_check_path: health` passes validation and builds
  the Probe URL `http://127.0.0.1:41248health`. Every Probe fails, the Backend
  never becomes Alive, and the only signal is the `Backend is Down` warning.
- **Fix:** reject a path that does not start with `/`, naming the key and the
  Backend's index, like `ErrInvalidHealthCheckStatus`.
- **Regression test:** `PrepareConfig` rejects `health_check_path: health`.

<a id="b8"></a>
### B8. cert_file without key_file (or the reverse) serves plain HTTP

- **Verified:** reproduced
- **Where:** [pkg/config/config.go:472-487](pkg/config/config.go#L472-L487), [internal/server/server.go:64](internal/server/server.go#L64)
- **What happens:** with only one of the two set, `PrepareConfig` succeeds and
  the HTTP/1.1 listener serves plain HTTP (`http2` is guarded by
  `ErrHttp2WithoutTls`). review.md M7 noted it ("Noted, not fixed"); it was
  never tracked.
- **Impact:** a typo in one key silently downgrades the listener to
  cleartext.
- **Fix:** a startup error naming the missing key.
- **Regression test:** `PrepareConfig` with only `cert_file`, and with only
  `key_file`, fails.

<a id="b9"></a>
### B9. Listeners share their port silently and cannot bind IPv6

- **Verified:** reproduced on Windows; Linux is code-read
- **Where:** [main.go:89](main.go#L89), [internal/monitoring/monitoring.go:158](internal/monitoring/monitoring.go#L158): `reuseport.Listen("tcp4", …)`
- **What happens:** fasthttp's `reuseport` sets `SO_REUSEPORT` on Linux
  (`SO_REUSEADDR` on Windows). A second listener on a port already in use
  succeeds instead of failing with "address already in use"; on Linux, two
  divisor processes running as the same user then split the connections
  between them. `tcp4` also makes an IPv6 `host` impossible: binding `[::1]`
  fails with `no suitable address found`.
- **Impact:** a forgotten old divisor (or a second unit) keeps taking traffic
  with the old config, and nothing reports it. divisor runs one process, so
  it gains nothing from port sharing.
- **Fix:** `net.Listen("tcp", addr)` for both listeners.
- **Regression test:** a second listener on the same address fails, and
  `host: "::1"` binds.

## Low

<a id="b10"></a>
### B10. process_cpu_percent is a lifetime average

- **Verified:** code-read
- **Where:** [internal/monitoring/monitoring.go:101-110](internal/monitoring/monitoring.go#L101-L110); gopsutil's `Process.CPUPercent` divides total CPU time by the time since the process started (`process/process.go:365-383` in gopsutil v4.26.9)
- **What happens:** `/stats` `cpu.process_percent` and the
  `process_cpu_percent` gauge report CPU usage averaged over the whole
  process lifetime. After a day of uptime a spike barely moves them.
  [docs/observability.md:143](docs/observability.md#L143) describes it as the
  process's CPU usage.
- **Fix:** keep one `*process.Process` for the collector and call
  `Percent(0)`, which measures since the previous call (every 5s from the
  poller); or keep the value and document it as a lifetime average.

<a id="b11"></a>
### B11. No read or idle timeout by default on the client listeners

- **Verified:** code-read; documented in [docs/configuration.md:119-121](docs/configuration.md#L119-L121)
- **Where:** [pkg/config/config.go:498-560](pkg/config/config.go#L498-L560) (no default for `read_timeout`/`idle_timeout`), [internal/server/server.go:84-93](internal/server/server.go#L84-L93) (no `ReadHeaderTimeout` on the HTTP/2 stack)
- **What happens:** `read_timeout` defaults to unlimited and `idle_timeout`
  follows it, so a client that sends headers slowly, or idles on a keep-alive
  connection, holds it forever (up to `server.concurrency`, 262144).
- **Impact:** a public listener on defaults is exposed to slow-header
  (slowloris) connection exhaustion. `examples/production.config.yaml` sets
  the timeouts; the defaults do not.
- **Fix (decision needed):** default `idle_timeout` and a bound on reading
  headers, and set `ReadHeaderTimeout` on the net/http server. Changing a
  default after 1.0 is a behavior change: pick the release and add a
  migration note.

<a id="b12"></a>
### B12. backends[].weight has no upper bound

- **Verified:** code-read, deliberately not run
- **Where:** [core/w-round-robin/w_round_robin.go:23-32](core/w-round-robin/w_round_robin.go#L23-L32)
- **What happens:** every Join allocates and shuffles `weight` slots per
  Backend. A typo such as `weight: 1000000000` allocates about 8 GB at
  startup.
- **Fix:** validate an upper bound (e.g. 1000) naming the key and Backend
  index, or reduce all weights by their greatest common divisor first.

<a id="b13"></a>
### B13. The monitoring dashboard needs the internet

- **Verified:** code-read
- **Where:** [internal/monitoring/index.go:11-13](internal/monitoring/index.go#L11-L13) (Google Fonts), [:98](internal/monitoring/index.go#L98) (Chart.js from cdnjs)
- **Impact:** in an air-gapped cluster the dashboard's charts stay blank, and
  every view makes third-party requests.
- **Fix:** embed Chart.js with `go:embed`, serve it from the Monitoring
  server, and use a system font.

<a id="b14"></a>
### B14. ip-hash keeps every Backend it ever Joined

- **Verified:** code-read
- **Where:** [core/ip-hash/ip_hash.go:19-21](core/ip-hash/ip_hash.go#L19-L21), [:54-61](core/ip-hash/ip_hash.go#L54-L61)
- **What happens:** `backendsByIndex` is never pruned, by design (a Pick may
  resolve a node from a ring snapshot taken before its Backend Left). A
  long-lived process whose Reloads keep adding Backends (frequent ConfigMap
  edits) keeps every removed `*Backend` reachable.
- **Fix:** let the ring node carry its Backend (a payload field on
  `consistent.Node`, or a generic node), so a Pick resolves from its own
  snapshot and the map goes away.

## Docs

<a id="d1"></a>
### D1. The Balancer guide recommends ip-hash and least-response-time without caveats

- **Where:** [examples/README.md:62-69](examples/README.md#L62-L69), the only guidance for choosing a Balancer (linked from [docs/configuration.md:42-43](docs/configuration.md#L42-L43))
- **What is wrong:** ip-hash is recommended for "Shopping carts, user
  sessions" and least-response-time for "Backends with varying performance",
  with no word on B1, B2, B3, or ip-hash collapsing every client behind a NAT
  or another load balancer onto one Backend.
- **Fix:** add the caveats, or link the limitations page
  ([features.md FD1](features.md#fd1)).

<a id="d2"></a>
### D2. Getting started understates the 502s before a Probe notices a Down Backend

- **Where:** [docs/getting-started.md:144-146](docs/getting-started.md#L144-L146)
- **What is wrong:** "one sent before the Probe notices may get a `502`": every
  request routed to the stopped Backend gets a 502 until the next Probe round
  (no retry on another Backend, see [features.md F1](features.md#f1)).
- **Fix:** say so, and give the window (`health_checker_time` plus
  `health_checker_timeout`).

<a id="d3"></a>
### D3. The benchmark is from 0.x

- **Where:** [benchmark/README.md](benchmark/README.md), linked from [README.md:94-96](README.md#L94-L96)
- **What is wrong:** last updated 2023-03-07: v0.x on Windows 10, Backends on
  the same machine, averages only (no p99), no comparison with another load
  balancer. It says nothing about 1.0's request path (Pool, Middleware
  context, Access log, streaming).
- **Fix:** rerun on Linux for 1.0 with an open-loop tool (wrk2 or k6),
  reporting p50/p99/p999 latency and CPU per 10k req/s, with nginx and HAProxy
  as baselines; or label the current page as 0.x results.
