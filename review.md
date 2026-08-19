# Code Review — divisor

**Date:** 2026-08-16 · **Scope:** `core/`, `internal/`, `middleware/`, `pkg/` (whole project, current working tree)
**Method:** four parallel reviewers (one per area), findings cross-deduplicated; the top critical findings were independently confirmed by two reviewers and manually re-verified against the source. Line numbers refer to the current working tree.

No spec/issue tracker exists for this repo, so this is a bug-focused review plus a code-smell pass — not a spec-conformance review. Findings marked *(judgement call)* are defensible-design questions, not hard bugs.

**Totals:** 4 critical · 8 high · 9 medium · 20 low · 6 smells

## Fix checklist

Tick items off as we fix them:

- [x] [C1 — `Stats()` panics when a backend is down at startup → process crash](#c1) ✅ fixed
- [x] [C2 — Data race on `servers`/`len` between requests and health checker](#c2) ✅ fixed
- [x] [C3 — Consistent-hash ring race + wrong removal order → handler panic](#c3) ✅ fixed
- [x] [C4 — A panicking middleware crashes the whole load balancer](#c4) ✅ fixed
- [ ] [H1 — Backends down at startup can never rejoin the pool](#h1)
- [ ] [H2 — Health-checker stop signal is never delivered](#h2)
- [ ] [H3 — Middleware errors silently discarded → client gets 200 OK](#h3)
- [ ] [H4 — Millisecond truncation breaks least-response-time](#h4)
- [ ] [H5 — Failed requests shrink the response-time average → traffic black hole](#h5)
- [ ] [H6 — Trailing slash/path in backend URL → invalid dial address](#h6)
- [ ] [H7 — Swallowed logger error → nil-logger panic at startup](#h7)
- [ ] [H8 — net/http adapter forwards stale Content-Length](#h8)
- [ ] [M1 — least-connection doesn't pick the least-connected server](#m1)
- [ ] [M2 — `isHostAlive` data race between `Stats()` and health checker](#m2)
- [x] [M3 — "All backends are down" panic makes transient outages permanent](#m3)
- [ ] [M4 — Virtual-node key collisions corrupt the ip-hash ring](#m4)
- [ ] [M5 — `OnResponse` cannot override backend errors as documented](#m5)
- [ ] [M6 — `https://` backends silently downgraded to plain HTTP](#m6)
- [ ] [M7 — Typed-nil `any` defeats server-startup failure check](#m7)
- [ ] [M8 — Connection-nominated headers not stripped (RFC 7230 §6.1)](#m8)
- [ ] [M9 — `$incremental` header race produces duplicate values](#m9)
- [ ] [L1–L20 — Low-severity bugs & sharp edges](#low)
- [ ] [S1–S6 — Code smells](#smells)

---

## Critical

<a id="c1"></a>
### C1. `Stats()` panics with index-out-of-range when any backend is down at startup → whole process crashes

*Cross-confirmed by two independent reviewers and manually verified.*

- **Where:** [core/round-robin/round-robin.go:111-114](core/round-robin/round-robin.go#L111-L114), [core/w-round-robin/w-round-robin.go:130-133](core/w-round-robin/w-round-robin.go#L130-L133), [core/ip-hash/ip-hash.go:119-122](core/ip-hash/ip-hash.go#L119-L122), [core/least-algorithm/least-algorithm.go:146-149](core/least-algorithm/least-algorithm.go#L146-L149), [core/random/random.go:109-112](core/random/random.go#L109-L112) — triggered from [internal/monitoring/monitoring.go:102-108](internal/monitoring/monitoring.go#L102-L108)
- **Bug:** `stats := make([]types.ProxyStat, len(serversMap))` sizes the slice by the number of *added* servers, but writes `stats[p.i]` where `p.i` is the backend's original index in `cfg.Backends`. Constructors `continue` past backends that fail the initial health check, so a surviving entry can have `p.i >= len(serversMap)`.
- **Failure scenario:** backends `[A, B]`; A is briefly down at boot. `serversMap` has one entry with `i=1`; `Stats()` builds a 1-element slice and executes `stats[1] = …` → panic. The monitoring goroutine calls `Stats()` immediately and every 5s, so the process crash-loops with no request traffic at all.
- **Fix:** `append` to the slice instead of indexing by config position (all five balancers).
- **Status: FIXED (2026-08-16).** Renamed `serverMap.i` → `statsIdx` and assign it `len(serversMap)` at registration time, so indexes are dense (0..n-1 over *added* servers) while `Stats()` output stays in deterministic config order. Regression test `TestStatsWhenBackendDownAtStartup` added to all five balancer test packages.

<a id="c2"></a>
### C2. Unsynchronized `servers` slice and `len` shared between request goroutines and the health checker

*Manually verified.*

- **Where:** [core/round-robin/round-robin.go:70-73](core/round-robin/round-robin.go#L70-L73) vs [:94-104](core/round-robin/round-robin.go#L94-L104); same pattern in [core/w-round-robin/w-round-robin.go:80-83](core/w-round-robin/w-round-robin.go#L80-L83)/[:105-123](core/w-round-robin/w-round-robin.go#L105-L123), [core/random/random.go:69-71](core/random/random.go#L69-L71)/[:92-102](core/random/random.go#L92-L102), [core/least-algorithm/least-algorithm.go:82-104](core/least-algorithm/least-algorithm.go#L82-L104)/[:125-138](core/least-algorithm/least-algorithm.go#L125-L138)
- **Bug:** the health-checker goroutine reassigns `servers` and mutates `len` while `next()` reads both from fasthttp request goroutines with no mutex/atomic (only the rotation counter `i` is atomic). A genuine Go memory-model data race (`go test -race` under load flags it).
- **Failure scenario:** one of two backends fails a health check. A request goroutine observes the new 1-element `servers` but the stale `len == 2`, computes `v%2 == 1`, indexes `servers[1]` → panic in the handler; fasthttp does not recover handler panics, so the process dies. w-round-robin additionally shuffles the slice **in place** on re-add ([w-round-robin.go:119-121](core/w-round-robin/w-round-robin.go#L119-L121)) while readers index the same backing array; least-algorithm can load a stale `lastIndex` past the shortened slice.
- **Fix:** guard `servers`/`len` with a `sync.RWMutex`, or publish immutable snapshots via `atomic.Pointer`. (Fixing via a shared base type also addresses S1.)
- **Status: FIXED (2026-08-16).** Lock-free copy-on-write snapshots via `atomic.Pointer[[]proxy.IProxyClient]` in all four slice-based balancers (chosen over RWMutex to keep the request hot path contention-free — `BenchmarkNext` ≈18.5 ns/op parallel on 12 threads). The separate `len` field is deleted; length always comes from the loaded snapshot, so a stale-`len`/new-slice mismatch is impossible. The health checker (single writer) builds a fresh slice (`RemoveByValue` already copies; re-add copies explicitly; w-round-robin shuffles the fresh copy before publishing) and `Store`s it. least-algorithm's `lastIndex` is bounds-checked against the loaded snapshot at read time. Regression test `TestNextConcurrentWithHealthCheck` added to all four packages (hammers selection while flapping a backend); run under `go test -race` on a cgo-enabled machine/CI for full verification. ip-hash's equivalent race lives in the consistent-hash ring → C3.

<a id="c3"></a>
### C3. Consistent-hash ring: no locking at all, and removal order makes `GetNode` return nil → interface-conversion panic

*Cross-confirmed by two independent reviewers and manually verified.*

- **Where:** [pkg/consistent/consistent.go:46-77](pkg/consistent/consistent.go#L46-L77), driven from [core/ip-hash/ip-hash.go:77-80](core/ip-hash/ip-hash.go#L77-L80) (request path) and [:101-114](core/ip-hash/ip-hash.go#L101-L114) (health checker)
- **Bug:** `numbers` (the ring) is appended, removed from (in-place shift via `helper.Remove`), and `sort.Sort`ed by the health checker while request goroutines run `sort.Search` + `c.numbers[i]` on it — no mutex. Worse, `RemoveNode` deletes from the `nodes` map *before* removing the hash from `numbers`, so the ring can point at a hash with no map entry.
- **Failure scenario:** a backend flaps down; mid-`RemoveNode` (map entry deleted, hash still in ring) a request hashes to it: `nodes.Load` returns `(nil, false)` and `node.(*Node)` at [consistent.go:76](pkg/consistent/consistent.go#L76) panics → handler panic → process crash. Even without the panic, in-place sort during concurrent reads silently routes IPs to wrong backends, breaking stickiness.
- **Fix:** add a `sync.RWMutex` (write-lock Add/Remove, read-lock Get), use copy-on-write for the slice, remove from `numbers` before deleting from `nodes`, and make `GetNode` handle a missed load. (See also M4, L9.)
- **Status: FIXED (2026-08-16).** Went lock-free (matching C2's design) instead of RWMutex: the ring is now an immutable `ringSnapshot{nodes map[uint32]*Node, numbers hashRing}` published via `atomic.Pointer`. `GetNode` does one `Load` and reads both structures from the same snapshot, so the map/ring can never disagree — the delete-ordering panic is structurally impossible, and the `sync.Map` + untyped `node.(*Node)` assertion is gone (typed map). `AddNode`/`RemoveNode` (single writer: the health checker) build fresh copies and `Store`. `GetNode` on an empty ring now returns `nil` instead of index-panicking (caller-side handling tracked by L9/M3). `ConsistentHash` became non-copyable, so ip-hash holds `*consistent.ConsistentHash` instead of a struct copy. Regression tests: `TestGetNodeConcurrentWithAddRemove` (flaps a node while hammering `GetNode`; panics under old code) and `TestGetNodeEmptyRing`. M4 (vnode key collisions) intentionally untouched.

<a id="c4"></a>
### C4. A panic inside user middleware at request time crashes the entire load balancer

- **Where:** [pkg/middleware/executor.go:123-140](pkg/middleware/executor.go#L123-L140) (`RunOnRequest`/`RunOnResponse`), invoked from [internal/proxy/proxy.go:69](internal/proxy/proxy.go#L69) and [:81](internal/proxy/proxy.go#L81)
- **Bug:** `NewExecutor` recovers panics at load time ([executor.go:43-47](pkg/middleware/executor.go#L43-L47)), but nothing guards the per-request invocation of interpreted user code — and fasthttp does not recover handler panics (verified against fasthttp v1.68.0).
- **Failure scenario:** a middleware does `ctx.Request.Header.Peek("X-Api-Key")[0]` (or an unchecked assertion) that only trips on certain requests. It passes load-time validation; the first request lacking the header panics through yaegi → handler → fasthttp → unrecovered panic terminates the process, dropping every in-flight connection. A single crafted client request can kill the LB. (The net/http HTTP/2 path recovers per-connection; the default fasthttp path is fatal.)
- **Fix:** wrap each `mw.OnRequest`/`mw.OnResponse` call in a deferred `recover()` that converts the panic into an error, mirroring the load-time guard.
- **Status: FIXED (2026-08-16).** Every `mw.OnRequest`/`mw.OnResponse` call now goes through `runProtected`, which recovers panics, logs them at error level with a stack trace, and returns them as `middleware panic: …` errors — same contract as a middleware returning an error. Placed in the executor so both the fasthttp path and the net/http (HTTP/2) adapter path are covered. Regression test `TestMiddlewarePanicRecovered` loads a middleware through yaegi that panics at request time (nil-slice index) and response time (explicit panic) and asserts both convert to errors without panicking. Note: until H3 is fixed, the resulting error is still discarded by the handler (client gets an empty 200 instead of a 500) — but the process no longer dies.

---

## High

<a id="h1"></a>
### H1. Backends that are down at startup can never rejoin the pool, even after recovering

- **Where:** [core/round-robin/round-robin.go:43-47](core/round-robin/round-robin.go#L43-L47) + [:92-107](core/round-robin/round-robin.go#L92-L107); same pattern in all five balancers (w-round-robin :45-49/:102-126, ip-hash :47-51/:99-115, least-algorithm :45-49/:123-142, random :42-46/:90-105)
- **Bug:** constructors skip dead backends without creating a proxy client or `serversMap` entry. `healthCheck` requires `serversMap[backendHash]` to exist in every branch — a missing entry means the fresh health status is silently discarded forever. This contradicts the documented intent ("Failed backends are marked but not removed, allowing recovery").
- **Failure scenario:** rolling deploy — backend B is restarting during the 2 seconds divisor boots. B recovers 30s later; the health checker sees it alive every cycle and never adds it. Traffic runs permanently degraded until divisor itself restarts.
- **Fix:** always create the proxy client and `serversMap` entry (marked `isHostAlive: false`, excluded from `servers`) so the existing re-add branch picks the backend up.

<a id="h2"></a>
### H2. `Shutdown()`'s health-checker stop signal is effectively never delivered

*Manually verified.*

- **Where:** [core/round-robin/round-robin.go:76-87](core/round-robin/round-robin.go#L76-L87) (loop) + [:132-137](core/round-robin/round-robin.go#L132-L137) (send); identical in the other four balancers
- **Bug:** `stopHealthChecker` is unbuffered; the checker polls it with `select`+`default` (spending nearly all time in `time.Sleep`), and `Shutdown` sends with `select`+`default`. With both sides non-blocking, the rendezvous almost never happens: `Shutdown` takes `default`, logs the misleading "Health checker already stopped", and the goroutine runs forever. The unit test only passes because it does a *blocking* send.
- **Failure scenario:** SIGTERM during full-stack teardown with backends already stopped: graceful shutdown "stops" the checker (no-op), the still-running checker finds the last backend dead and executes `panic("All backends are down")` — the graceful shutdown becomes a crash with nonzero exit. Any embedding use leaks a goroutine + health-check HTTP calls per balancer.
- **Fix:** `close()` the channel in `Shutdown` and replace the sleep loop with a `time.Ticker` selected against it.

<a id="h3"></a>
### H3. Middleware short-circuit errors are silently discarded — rejected requests get a default 200 OK

*Cross-confirmed by two independent reviewers.*

- **Where:** [internal/proxy/proxy.go:68-73](internal/proxy/proxy.go#L68-L73) and [:80-85](internal/proxy/proxy.go#L80-L85); discarded at every call site ([core/round-robin/round-robin.go:66](core/round-robin/round-robin.go#L66) etc., all `//nolint:errcheck`) and in [internal/proxy/nethttp_adapter.go:29](internal/proxy/nethttp_adapter.go#L29)
- **Bug:** returning an error is the middleware contract's only short-circuit mechanism, and the README states "The error is returned to the client." In reality the error propagates up, every consumer discards it, nothing writes it (or any status) to `ctx.Response`, and it isn't even logged.
- **Failure scenario:** an auth middleware does `return errors.New("unauthorized")` without manually setting the response (the pattern the README implies). The request is correctly not forwarded, but the client receives the untouched pooled response — **HTTP 200, empty body**. Every denied request looks like success to clients, caches, and monitoring.
- **Fix:** in `ReverseProxyHandler` (or the `Serve` closures), translate a non-nil middleware error into a client-visible response (e.g. 403/500 + body when the middleware didn't set a status itself) and log it.

<a id="h4"></a>
### H4. Response times truncated to whole milliseconds — least-response-time collapses to a single backend

*Cross-confirmed by two independent reviewers.*

- **Where:** [internal/proxy/proxy.go:93](internal/proxy/proxy.go#L93) (`time.Since(s).Milliseconds()`), [:153-161](internal/proxy/proxy.go#L153-L161); consumed by [core/least-algorithm/least-algorithm.go:94-104](core/least-algorithm/least-algorithm.go#L94-L104)
- **Bug:** every sub-millisecond response adds 0 to `totalResTime`, so `AvgResponseTime()` returns 0; `leastResponseTimeNext` only switches on *strictly less*, and nothing beats 0.
- **Failure scenario:** typical LAN/localhost backends answering in <1ms: all averages stay 0 forever and every request goes to `servers[lastIndex]` — 100% of traffic to one backend, zero balancing. Conversely, one slow request leaves a backend with a positive never-decaying average, permanently deprioritized against 0-average peers.
- **Fix:** accumulate `Microseconds()`/`Nanoseconds()` (drop the `rt == 0` special case), consider a moving average, and treat a 0-sample server as unknown rather than best.

<a id="h5"></a>
### H5. Failed requests counted in the denominator but not the numerator — a failing backend becomes a traffic black hole

- **Where:** [internal/proxy/proxy.go:58](internal/proxy/proxy.go#L58) (count at entry), [:88-93](internal/proxy/proxy.go#L88-L93) (time only on success), [:153-161](internal/proxy/proxy.go#L153-L161); consumed by [core/least-algorithm/least-algorithm.go:94-104](core/least-algorithm/least-algorithm.go#L94-L104)
- **Bug:** `totalRequestCount` increments for every request, but `totalResTime` is only added on full success — every failure drags the average *down*; in-flight requests also inflate the denominator early.
- **Failure scenario:** under `least-response-time`, a backend starts refusing connections between health checks. Each fast failure shrinks its average, so it receives an ever-growing share of traffic for up to `health_checker_time` (default 30s) until the checker removes it.
- **Fix:** record elapsed time on every path (including errors), or track successes in a separate counter used as the denominator.

<a id="h6"></a>
### H6. Protocol stripping leaves path/trailing slash in the backend URL → invalid `HostClient.Addr`, every proxied request fails

- **Where:** [pkg/config/config.go:42](pkg/config/config.go#L42) (`protocolRegex`), [:195](pkg/config/config.go#L195); consumed at [internal/proxy/proxy.go:174](internal/proxy/proxy.go#L174) (`Addr: backend.Url`)
- **Bug:** only the scheme prefix is stripped; a path, trailing slash, query, or userinfo survives into `b.Url`, which is used verbatim as `fasthttp.HostClient.Addr` (must be a dialable `host:port`). Nothing validates it.
- **Failure scenario:** `url: http://localhost:8080/` (very common) yields `Addr = "localhost:8080/"`; dialing fails on port `"8080/"` so every proxied request returns 500 — while the health check (`http://localhost:8080//`) can still return 200 on slash-merging servers (nginx), so the backend is happily kept in rotation.
- **Fix:** `url.Parse` the backend URL and extract only `Host`, erroring on anything with a path/query or malformed `host:port`.

<a id="h7"></a>
### H7. Logger build error swallowed → nil logger passed to `zap.ReplaceGlobals` → startup panic

- **Where:** [pkg/logger/logger.go:29-30](pkg/logger/logger.go#L29-L30) (`logger, _ := config.Build()`), fed by [pkg/helper/helper.go:83-92](pkg/helper/helper.go#L83-L92)
- **Bug:** if `config.Build()` fails (log sink can't be opened), the error is discarded and nil goes into `zap.ReplaceGlobals`, which immediately calls `logger.Sugar()` → nil-pointer panic (verified in zap v1.27.1). `CreateLogDirIfNotExist` makes this reachable: a permission error from `os.Stat` is not `ErrNotExist`, so it returns nil and the unwritable path is used instead of falling back to `./divisor.log`.
- **Failure scenario:** on Linux, `/var/log/divisor/` exists from an earlier root run; the process now runs as non-root. `Build` fails to open the sink and `main.go:32` panics with a nil dereference before any usable error message.
- **Fix:** check the `Build` error and fall back to a stdout-only config; treat any stat error other than "not exist" as failure in `CreateLogDirIfNotExist`.

<a id="h8"></a>
### H8. net/http adapter forwards stale Content-Length — middleware body rewrites get truncated or hang

- **Where:** [internal/proxy/nethttp_adapter.go:31-40](internal/proxy/nethttp_adapter.go#L31-L40)
- **Bug:** the adapter copies the backend's parsed `Content-Length` into `w.Header()`, but fasthttp's `Response.SetBody/SetBodyString` (what an `OnResponse` middleware uses) does **not** update the stored content length — only `Response.Write` recomputes it, which the adapter path never calls.
- **Failure scenario:** http2 mode + a middleware rewriting a response body: backend returns `"hello"` (CL=5), middleware sets `"goodbye world"` (13 bytes). The adapter sends `Content-Length: 5`; net/http truncates at 5 bytes and returns `http.ErrContentLength` (silently ignored). The client receives `"goodb"`. A shorter rewrite ends the stream early — client errors/hangs.
- **Fix:** skip `Content-Length` (and `Trailer`) in the header copy loop and let net/http derive it from the bytes written, or set it from `len(ctx.Response.Body())`.

---

## Medium

<a id="m1"></a>
### M1. least-connection picks the first improvement, not the least-connected server

- **Where:** [core/least-algorithm/least-algorithm.go:82-92](core/least-algorithm/least-algorithm.go#L82-L92)
- **Bug:** the loop `break`s at the first server with fewer pending requests than the baseline, instead of scanning for the minimum. (`leastResponseTimeNext` right below correctly omits the `break`, confirming it's unintended.)
- **Failure scenario:** 3 backends with pending [5, 3, 1], `lastIndex=0`: picks the 3-pending server, never considers the 1-pending one. The truly least-loaded backend is systematically under-utilized. Existing tests use only 2 backends, masking it.
- **Fix:** remove the `break`.

<a id="m2"></a>
### M2. `isHostAlive` read by `Stats()` while written by the health checker — data race

- **Where:** [core/round-robin/round-robin.go:120](core/round-robin/round-robin.go#L120) vs [:96](core/round-robin/round-robin.go#L96)/[:105](core/round-robin/round-robin.go#L105); same in all five balancers
- **Bug:** the monitoring goroutine calls `Stats()` every 5s and on `/stats` requests, reading `serverMap.isHostAlive` unsynchronized against health-checker writes.
- **Failure scenario:** monitoring scrape concurrent with a health-state flip: torn/stale liveness reporting; any future field on `serverMap` inherits the race.
- **Fix:** fold into the mutex from C2, or make `isHostAlive` an `atomic.Bool`.

<a id="m3"></a>
### M3. `panic("All backends are down")` irrecoverably kills the proxy *(judgement call — deliberate but harsh)*

- **Where:** [core/round-robin/round-robin.go:99-101](core/round-robin/round-robin.go#L99-L101); same in all five balancers
- **Bug:** when the last live backend fails a health check, the checker goroutine panics and crashes the process — the code's own recovery mechanism (re-add next cycle) can never run.
- **Failure scenario:** all backends share one database that restarts for 60s → every backend fails one health round → divisor panics and stays dead after the backends recover, converting a transient outage into a permanent one. In `random`, a request racing the `len--` can also hit `rand.Uint32N(0)`, which panics.
- **Fix:** keep serving 502/503 while the pool is empty and let the health checker re-add backends when they return.

<a id="m4"></a>
### M4. Virtual-node keys `string(rune(Id+i)) + Addr` collide for duplicate backend URLs — ring corruption and request-path panic

*Cross-confirmed by two independent reviewers.*

- **Where:** [pkg/consistent/consistent.go:48](pkg/consistent/consistent.go#L48) and [:57](pkg/consistent/consistent.go#L57); node Ids assigned at [core/ip-hash/ip-hash.go:53](core/ip-hash/ip-hash.go#L53)
- **Bug:** nodes with the same `Addr` generate overlapping rune ranges (Id=0,i=1 collides with Id=1,i=0), so `nodes.Store` overwrites entries while `numbers` accumulates duplicate hashes; removing one node deletes map entries the other node's ring positions still reference.
- **Failure scenario:** the same backend URL listed twice in config (accepted without complaint; a normal way to double a backend's share in other algorithms). When one duplicate flaps down, `RemoveNode` deletes the shared hashes; the next request landing on a leftover ring entry gets `nodes.Load` → nil → panic at [consistent.go:76](pkg/consistent/consistent.go#L76). Even without removal, distribution is silently skewed.
- **Fix:** collision-free key, e.g. `fmt.Sprintf("%d|%d|%s", node.Id, i, node.Addr)`; optionally reject duplicate backend URLs in `PrepareConfig`.

<a id="m5"></a>
### M5. `OnResponse` cannot override backend errors as documented — `serverError` clobbers the custom response and leaks the dial error

- **Where:** [internal/proxy/proxy.go:80-91](internal/proxy/proxy.go#L80-L91), [:114-120](internal/proxy/proxy.go#L114-L120); contract in README ("OnResponse can override backend errors")
- **Bug:** when `proxy.Do` fails and `OnResponse` writes a fallback response then returns nil (the intuitive "handled" signal), `serverError()` unconditionally overwrites status to 500, sets `Connection: close`, and replaces the body with the raw dial error. The only way to preserve a crafted response is returning a non-nil error — which is then dropped (H3).
- **Failure scenario:** middleware serves a cached page on backend failure, returns nil → client gets `{"message":"dial tcp …: connection refused"}` with 500 instead of the fallback (also leaking internals; see L3).
- **Fix:** give the contract an explicit "handled" signal (sentinel error or response-modified check) and skip `serverError` when it's set.

<a id="m6"></a>
### M6. `https://` backend URLs silently accepted, then treated as plain HTTP everywhere

- **Where:** [pkg/config/config.go:42](pkg/config/config.go#L42)+[:195](pkg/config/config.go#L195) (strips `https://` like `http://`), [:63-65](pkg/config/config.go#L63-L65) (health URL hardcodes `http://`); [internal/proxy/proxy.go:102](internal/proxy/proxy.go#L102), [:173-180](internal/proxy/proxy.go#L173-L180) (no `IsTLS`)
- **Bug:** a TLS-only backend can never work, but the config layer drops the scheme without error.
- **Failure scenario:** `url: https://api.example.com` → health check GETs the plain-HTTP URL → 301 redirect → treated as down → backend never added; with all backends https, startup fails with the unrelated-looking "No available servers". If the health endpoint answers 200 over HTTP, traffic is proxied **unencrypted** to port 80.
- **Fix:** either reject `https://` URLs with a clear error, or preserve the scheme, set `HostClient.IsTLS`, and use an https health-check URL.

<a id="m7"></a>
### M7. Typed-nil `any` comparison defeats the server-startup failure check

- **Where:** [main.go:82-94](main.go#L82-L94), [:159-163](main.go#L159-L163) (found tracing `pkg/config`'s Http2 constant; kept here because the fix belongs with config/server wiring)
- **Bug:** `startNetHttpServer` returns a typed nil `*http.Server` when `http2.ConfigureServer` fails; stored in an `any`, the interface is non-nil, so `if server == nil` never fires.
- **Failure scenario:** http2 configuration error → process runs indefinitely with no HTTP listener; later `performGracefulShutdown` calls `Shutdown` on the nil `*http.Server` and panics.
- **Fix:** return `(server, error)` from both start functions and check the error.

<a id="m8"></a>
### M8. Headers nominated in the `Connection` header are not removed (RFC 7230 §6.1)

- **Where:** [internal/proxy/proxy.go:97-112](internal/proxy/proxy.go#L97-L112), [internal/proxy/nethttp_adapter.go:46-53](internal/proxy/nethttp_adapter.go#L46-L53)
- **Bug:** only the fixed `hopHeaders` list is deleted; the `Connection` header's *value* is never parsed to remove the headers it nominates (the comment at proxy.go:29-31 acknowledges the requirement; Go's `httputil.ReverseProxy` does both).
- **Failure scenario:** backend replies `Connection: X-Internal-Debug` + `X-Internal-Debug: <internal data>`; the internal header is forwarded to the client. Inbound, a client's `Connection: X-Secret` makes `X-Secret` reach the backend as if end-to-end.
- **Fix:** before deleting `Connection`, split its value on commas and delete each nominated header on both request and response paths.

<a id="m9"></a>
### M9. `$incremental` header is not atomic — concurrent requests get duplicate values

- **Where:** [internal/proxy/proxy.go:58](internal/proxy/proxy.go#L58) and [:129-130](internal/proxy/proxy.go#L129-L130)
- **Bug:** `atomic.AddUint64` at handler entry discards the return value; the counter is re-read later with `atomic.LoadUint64` — a read-after-add race.
- **Failure scenario:** request A: Add→1; request B: Add→2; both Load→2. Both backend requests carry the value 2 and 1 never appears — duplicated/skipped sequence numbers break request correlation.
- **Fix:** capture `v := atomic.AddUint64(…)` at entry and use `v` in `setCustomHeaders`.

---

<a id="low"></a>
## Low — bugs & sharp edges

### L1. `X-Forwarded-For` overwrites any existing chain *(judgement call)*
[internal/proxy/proxy.go:104](internal/proxy/proxy.go#L104) — an incoming XFF from an upstream proxy/CDN is replaced, not appended; behind another proxy the real client IP is lost. Overwriting is a defensible anti-spoofing default. **Fix:** append (`prior + ", " + clientIP`) or make it configurable.

### L2. `$time` stamps local time with a literal `Z` (UTC) suffix
[internal/proxy/proxy.go:128](internal/proxy/proxy.go#L128) — `time.Now().Local().Format("2006-01-02T15:04:05.000Z")`: `Z` is a literal in that layout, so a UTC+3 host claims `12:00Z` when UTC is 09:00. **Fix:** `time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")`.

### L3. `serverError` builds JSON by concatenation without escaping, and leaks internal error detail
[internal/proxy/proxy.go:114-120](internal/proxy/proxy.go#L114-L120) — invalid JSON when the error contains `"` or `\`; exposes backend addresses/dial errors to clients. **Fix:** `json.Marshal` a generic message; log detail server-side.

### L4. Monitoring stats goroutine can never be stopped, and starts before the listener bind succeeds
[internal/monitoring/monitoring.go:102-108](internal/monitoring/monitoring.go#L102-L108), [:137-141](internal/monitoring/monitoring.go#L137-L141) — no stop channel; if `reuseport.Listen` fails it polls gopsutil + `Stats()` forever; keeps touching balancer state during/after graceful shutdown. **Fix:** `time.Ticker` + done channel wired into shutdown; start only after `Listen` succeeds.

### L5. Transient gopsutil error zeroes all global Prometheus gauges
[internal/monitoring/monitoring.go:50-77](internal/monitoring/monitoring.go#L50-L77), [internal/monitoring/prometheus.go:69-77](internal/monitoring/prometheus.go#L69-L77) — one failed collection publishes a zero-valued snapshot (CPU/mem/goroutines → 0), firing false alerts. **Fix:** skip the update on error, keep last values.

### L6. net/http adapter ignores client cancellation *(judgement call)*
[internal/proxy/nethttp_adapter.go:24-41](internal/proxy/nethttp_adapter.go#L24-L41) — `r.Context()` never consulted; a disconnected HTTP/2 client leaves the backend request running, holding a connection. **Fix:** watch `r.Context().Done()` with `DoDeadline`/abort, or document the limitation.

### L7. net/http adapter drops client-sent trailer values
[internal/proxy/nethttp_adapter.go:89-96](internal/proxy/nethttp_adapter.go#L89-L96) — `r.Trailer` is read before the body is consumed (net/http populates values only after full body read), so actual trailer values are never forwarded. **Fix:** forward trailers after body consumption, or drop trailer support explicitly.

### L8. Streaming responses (SSE/long-poll) are fully buffered and never reach the client
[internal/proxy/proxy.go:76](internal/proxy/proxy.go#L76) (`HostClient.Do` reads the whole response; no `StreamResponseBody`), [internal/proxy/nethttp_adapter.go:40](internal/proxy/nethttp_adapter.go#L40) (no `http.Flusher` path) — an SSE backend never terminates its body, so `Do` blocks indefinitely and nothing is flushed. **Fix:** enable `StreamResponseBody` and stream/flush in both paths, or document streaming as unsupported.

### L9. `GetNode` has no empty-ring guard
[pkg/consistent/consistent.go:67-77](pkg/consistent/consistent.go#L67-L77) — empty `numbers` → `c.numbers[0]` panics. Today reachable only inside C3's race window, but any future caller with an empty ring panics on the request path. **Fix:** return nil on empty ring; handle in callers.

### L10. Empty backend `url` passes validation
[pkg/config/config.go:192-227](pkg/config/config.go#L192-L227) — a backend with no URL is accepted (the package's own tests construct `Backend{}` and get nil errors); the failure surfaces later as a confusing empty-Addr health-check warning. **Fix:** error when `b.Url` is empty after stripping.

### L11. `GetLogFolder` Windows fallback is dead code — logs can land in `\divisor\` at the drive root
[pkg/helper/helper.go:71-74](pkg/helper/helper.go#L71-L74) — `os.Getenv("LocalAppData") + "\\divisor\\"` is never `""`, so the empty-check can't fire; with `LocalAppData` unset (service accounts) the dir becomes `\divisor\` at the drive root instead of falling back to `./divisor.log`. **Fix:** check the env var before appending.

### L12. `tcp_keepalive_period` config option is a silent no-op
[pkg/config/config.go:77](pkg/config/config.go#L77), wired at [main.go:114](main.go#L114) — fasthttp applies the period only when `Server.TCPKeepalive` is true, which divisor never sets. **Fix:** set `TCPKeepalive: true` when a period is configured.

### L13. Unknown `http_version` values silently coerced to `http1.1`
[pkg/config/config.go:230-232](pkg/config/config.go#L230-L232) — any typo (`HTTP2`, `h2`, `http/2`) silently becomes http1.1; the user believes HTTP/2 is active. **Fix:** error on values other than `""`, `"http1.1"`, `"http2"`.

### L14. Health check accepts only exactly HTTP 200
[pkg/http/http.go:39](pkg/http/http.go#L39) — backends answering 204/301/302 on their health path are permanently down (fasthttp doesn't follow redirects), not configurable. **Fix:** accept 2xx or make the expected status configurable.

### L15. Yaegi `Eval` failure masked by `ErrNewFunctionNotFound`
[pkg/middleware/executor.go:92-95](pkg/middleware/executor.go#L92-L95) — any `Eval` error is replaced wholesale, discarding the underlying cause; operators debug the wrong thing. **Fix:** wrap: `fmt.Errorf("%w: %v", ErrNewFunctionNotFound, err)`.

### L16. Dead middleware validation error variables
[pkg/middleware/executor.go:19-25](pkg/middleware/executor.go#L19-L25) — `ErrOnRequestFunctionNotFound/NotValid` etc. declared, never referenced; they advertise checks that don't exist. **Fix:** delete or implement.

### L17. Duplicated middleware validation with divergent error values
[pkg/middleware/executor.go:53-59](pkg/middleware/executor.go#L53-L59) vs [pkg/config/config.go:257-272](pkg/config/config.go#L257-L272) — same empty/both-set checks in two places with different error values; the executor's copy is unreachable in the production startup path. **Fix:** validate in one place.

### L18. Middleware contract exposes the pooled `RequestCtx` with no retention warning
[middleware/middleware.go:7-13](middleware/middleware.go#L7-L13) — fasthttp recycles `RequestCtx`; a middleware that captures it in a goroutine reads a recycled ctx serving a different request — cross-request data leakage. **Fix:** document the no-retention rule in the interface and README.

### L19. `OnResponse` chain runs in registration order, not reverse (onion) order *(judgement call — undocumented)*
[pkg/middleware/executor.go:133-140](pkg/middleware/executor.go#L133-L140) — outer middleware never observes inner middleware's response mutations. **Fix:** iterate in reverse for `RunOnResponse` and document the ordering.

### L20. Middleware instances are shared across all request goroutines — undocumented
[pkg/middleware/executor.go:61](pkg/middleware/executor.go#L61), [internal/proxy/proxy.go:52](internal/proxy/proxy.go#L52) — the per-middleware yaegi setup avoids the classic concurrent-Eval hazard (same pattern as Traefik plugins), but each middleware is a single shared instance: any mutable field in a user's struct is raced by concurrent requests with no warning in the contract. **Fix:** document that stateful middleware must synchronize internally.

---

<a id="smells"></a>
## Code smells (judgement calls)

### S1. Duplicated Code / Shotgun Surgery: five near-identical balancer implementations
`core/round-robin`, `core/w-round-robin`, `core/random`, `core/least-algorithm`, `core/ip-hash` — `serverMap`, constructor loop, `healthChecker`, `healthCheck`, `Stats()`, and `Shutdown()` are near-verbatim copies (the `healthChecker` loop is byte-identical apart from the receiver). Concrete cost: **C1, C2, H1, H2, and M2 each require the same fix in five files.** Extract a shared base type embedding servers/serversMap/health-checking/Stats/Shutdown, with algorithms supplying only `next()` — ideally *before* fixing those bugs, so each fix lands once.

### S2. Mysterious Names: `len`, `i`, `lastIndex`
[core/round-robin/round-robin.go:29-30](core/round-robin/round-robin.go#L29-L30) (`len`, `i`), [core/w-round-robin/w-round-robin.go:31](core/w-round-robin/w-round-robin.go#L31) (`len` actually means *sum of weights*), [core/least-algorithm/least-algorithm.go:31](core/least-algorithm/least-algorithm.go#L31) (`lastIndex` actually stores "index of the current least server"), `serverMap.i` everywhere (config index — load-bearing for C1). These names actively obscured C1 and M1; rename to `aliveCount`/`totalWeight`, `configIndex`, `leastIndex`.

### S3. `FindIndex` returns `(0, err)` on miss — a valid-index sentinel
[pkg/helper/helper.go:48-56](pkg/helper/helper.go#L48-L56) — index 0 is a legitimate result, so any caller dropping the error deletes element 0. The sole current caller checks, but the API invites the bug. Return `-1` or `(int, bool)`.

### S4. Log-path helpers misplaced in `pkg/helper` (low cohesion)
[pkg/helper/helper.go:58-92](pkg/helper/helper.go#L58-L92) — `GetLogFile`/`GetLogFolder`/`CreateLogDirIfNotExist` exist solely for `pkg/logger` inside an otherwise generic utility package. Moving them makes H7 + L11 a single-package fix.

### S5. Repeated type switches on `server any`
[internal/monitoring/monitoring.go:45,84-91](internal/monitoring/monitoring.go#L84-L91) and [main.go:185-196](main.go#L185-L196) — parallel `case *fasthttp.Server / case *http.Server` switches in every consumer; a small interface (`OpenConnectionsCounter`/`Shutdowner`) removes both and fixes the M7 typed-nil trap structurally.

### S6. net/http adapter re-derives the handler closure on every request
[internal/proxy/nethttp_adapter.go:29](internal/proxy/nethttp_adapter.go#L29) — `a.Balancer.Serve()(&ctx)` constructs a new closure per request; every `Serve()` is a pure factory that main.go calls once and reuses. Cache `a.handler = balancer.Serve()` in the constructor.

---

## Suggested fix order

1. **S1 first (extract shared balancer base)** — it turns five-file fixes into one-file fixes for everything below.
2. **C1 + C2 + M2 + H1 + H2** — one coherent change to the shared base: append-based `Stats()`, RWMutex (or atomic snapshot) around `servers`/`len`/`isHostAlive`, always-register backends, ticker + closed channel for shutdown.
3. **C3 + M4 + L9** — one change to `pkg/consistent`: RWMutex, copy-on-write ring, remove-before-delete ordering, collision-free keys, empty-ring guard.
4. **C4 + H3 + M5** — one change to the middleware execution path: per-request `recover()`, error-to-response translation, explicit "handled" signal.
5. **H4 + H5 + M1 + M9** — metrics/selection correctness in `internal/proxy` + `least-algorithm`.
6. **H6 + H7 + M6 + M7** — config/startup robustness.
7. **H8 and the rest** — adapter and low-severity items, batched as convenient.
