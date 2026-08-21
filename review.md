# Code Review — divisor

**Date:** 2026-08-16 · **Scope:** `core/`, `internal/`, `middleware/`, `pkg/` (whole project, current working tree)
**Method:** four parallel reviewers (one per area), findings cross-deduplicated; the top critical findings were independently confirmed by two reviewers and manually re-verified against the source. Line numbers refer to the current working tree (refs refreshed 2026-08-20 after the 503/all-backends-down, proxy_timeout/max_request_body_size, and integration-suite changes; in findings marked fixed, remaining line refs describe the pre-fix tree).

No spec/issue tracker exists for this repo, so this is a bug-focused review plus a code-smell pass — not a spec-conformance review. Findings marked *(judgement call)* are defensible-design questions, not hard bugs.

**Totals:** 4 critical · 8 high · 9 medium · 20 low · 6 smells — fixed so far: C1–C4, H1–H8, M3, M6, L9–L11, S4

## Fix checklist

Tick items off as we fix them:

- [x] [C1 — `Stats()` panics when a backend is down at startup → process crash](#c1) ✅ fixed
- [x] [C2 — Data race on `servers`/`len` between requests and health checker](#c2) ✅ fixed
- [x] [C3 — Consistent-hash ring race + wrong removal order → handler panic](#c3) ✅ fixed
- [x] [C4 — A panicking middleware crashes the whole load balancer](#c4) ✅ fixed
- [x] [H1 — Backends down at startup can never rejoin the pool](#h1) ✅ fixed
- [x] [H2 — Health-checker stop signal is never delivered](#h2) ✅ fixed
- [x] [H3 — Middleware errors silently discarded → client gets 200 OK](#h3) ✅ fixed
- [x] [H4 — Millisecond truncation breaks least-response-time](#h4) ✅ fixed
- [x] [H5 — Failed requests shrink the response-time average → traffic black hole](#h5) ✅ fixed
- [x] [H6 — Trailing slash/path in backend URL → invalid dial address](#h6) ✅ fixed
- [x] [H7 — Swallowed logger error → nil-logger panic at startup](#h7) ✅ fixed
- [x] [H8 — net/http adapter forwards stale Content-Length](#h8) ✅ fixed
- [ ] [M1 — least-connection doesn't pick the least-connected server](#m1)
- [ ] [M2 — `isHostAlive` data race between `Stats()` and health checker](#m2)
- [x] [M3 — "All backends are down" panic makes transient outages permanent](#m3)
- [ ] [M4 — Virtual-node key collisions corrupt the ip-hash ring](#m4)
- [ ] [M5 — `OnResponse` cannot override backend errors as documented](#m5)
- [x] [M6 — `https://` backends silently downgraded to plain HTTP](#m6) ✅ fixed with H6
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
- **Status: FIXED.** Constructors now always create the proxy client and `serversMap` entry; a down backend is registered `isHostAlive: false` and excluded from rotation ([core/round-robin/round-robin.go:43-54](core/round-robin/round-robin.go#L43-L54)), so the health checker's re-add branch ([:113-121](core/round-robin/round-robin.go#L113-L121)) picks it up when it recovers — same in all five balancers. Covered by the integration suite's startup-down-backend-rejoins scenario.

<a id="h2"></a>
### H2. `Shutdown()`'s health-checker stop signal is effectively never delivered

*Manually verified.*

- **Where:** [core/round-robin/round-robin.go:86-98](core/round-robin/round-robin.go#L86-L98) (loop) + [:146-151](core/round-robin/round-robin.go#L146-L151) (send); identical in the other four balancers
- **Bug:** `stopHealthChecker` is unbuffered; the checker polls it with `select`+`default` (spending nearly all time in `time.Sleep`), and `Shutdown` sends with `select`+`default`. With both sides non-blocking, the rendezvous almost never happens: `Shutdown` takes `default`, logs the misleading "Health checker already stopped", and the goroutine runs forever. The unit test only passes because it does a *blocking* send.
- **Failure scenario:** SIGTERM during graceful shutdown: `Shutdown` "stops" the checker (no-op, logs the misleading debug line) and the goroutine keeps running its sleep/health-check loop forever — a goroutine leak plus ongoing health-check HTTP calls per balancer. (Since M3's fix the last-backend-dead case only logs a warning instead of panicking, so the stale checker no longer crashes the process — but it still never stops.)
- **Fix:** `close()` the channel in `Shutdown` and replace the sleep loop with a `time.Ticker` selected against it.
- **Status: FIXED (2026-08-20).** `stopHealthChecker` is now a `chan struct{}` that `Shutdown` *closes* (guarded by a `sync.Once`, so repeated shutdowns stay idempotent) instead of trying a non-blocking send, and the checker loop selects that channel against a `time.Ticker` instead of sleeping ([core/round-robin/round-robin.go:90-108](core/round-robin/round-robin.go#L90-L108), [:155-158](core/round-robin/round-robin.go#L155-L158)) — same in all five balancers. Each checker also closes a `healthCheckerDone` channel on return and `Shutdown` waits on it, so shutdown no longer races ahead of in-flight health checks; the checker re-tests the stop channel between backends via the new `helper.IsClosed`, and the wait itself is capped by `types.HealthCheckerStopTimeout` (5s) so a hung probe can't eat the 30s graceful-shutdown budget. Regression test `TestShutdownStopsHealthChecker` added to all five balancer packages (asserts the health-check count is frozen 50 ms after `Shutdown`; fails on the old code), plus `TestIsClosed` in `pkg/helper`. The misleading "Health checker already stopped" debug line is gone.

<a id="h3"></a>
### H3. Middleware short-circuit errors are silently discarded — rejected requests get a default 200 OK

*Cross-confirmed by two independent reviewers.*

- **Where:** [internal/proxy/proxy.go:71-76](internal/proxy/proxy.go#L71-L76) and [:86-91](internal/proxy/proxy.go#L86-L91); discarded at every call site ([core/round-robin/round-robin.go:73](core/round-robin/round-robin.go#L73) etc., all `//nolint:errcheck`) and in [internal/proxy/nethttp_adapter.go:54](internal/proxy/nethttp_adapter.go#L54)
- **Bug:** returning an error is the middleware contract's only short-circuit mechanism, and the README states "The error is returned to the client." In reality the error propagates up, every consumer discards it, nothing writes it (or any status) to `ctx.Response`, and it isn't even logged.
- **Failure scenario:** an auth middleware does `return errors.New("unauthorized")` without manually setting the response (the pattern the README implies). The request is correctly not forwarded, but the client receives the untouched pooled response — **HTTP 200, empty body**. Every denied request looks like success to clients, caches, and monitoring.
- **Fix:** in `ReverseProxyHandler` (or the `Serve` closures), translate a non-nil middleware error into a client-visible response (e.g. 403/500 + body when the middleware didn't set a status itself) and log it.
- **Status: FIXED (2026-08-20).** Both middleware error paths in `ReverseProxyHandler` now go through `middlewareError`, which logs the error and — when the response is still the untouched pooled default (status 200, empty body) — answers `500` with a `json.Marshal`ed `{"message": …}` body ([internal/proxy/proxy.go:125-148](internal/proxy/proxy.go#L125-L148)). A middleware that wrote its own status or body keeps it, so the existing "middleware handles the backend error" pattern is unaffected. The net/http (HTTP/2) path inherits the fix, since the adapter copies whatever the handler left in `ctx.Response`. Regression test `TestMiddlewareErrorReachesClient` covers both paths, the crafted-response carve-out, and JSON escaping of quotes/backslashes in the message. README's lifecycle section now states the concrete status/body. Note: an `OnResponse` error still cannot replace a *successful* backend response (the response is non-default, so it is kept) — giving the contract an explicit "handled" signal is M5.

<a id="h4"></a>
### H4. Response times truncated to whole milliseconds — least-response-time collapses to a single backend

*Cross-confirmed by two independent reviewers.*

- **Where:** [internal/proxy/proxy.go:99](internal/proxy/proxy.go#L99) (`time.Since(s).Milliseconds()`), [:197-205](internal/proxy/proxy.go#L197-L205); consumed by [core/least-algorithm/least-algorithm.go:109-127](core/least-algorithm/least-algorithm.go#L109-L127)
- **Bug:** every sub-millisecond response adds 0 to `totalResTime`, so `AvgResponseTime()` returns 0; `leastResponseTimeNext` only switches on *strictly less*, and nothing beats 0.
- **Failure scenario:** typical LAN/localhost backends answering in <1ms: all averages stay 0 forever and every request goes to `servers[lastIndex]` — 100% of traffic to one backend, zero balancing. Conversely, one slow request leaves a backend with a positive never-decaying average, permanently deprioritized against 0-average peers.
- **Fix:** accumulate `Microseconds()`/`Nanoseconds()` (drop the `rt == 0` special case), consider a moving average, and treat a 0-sample server as unknown rather than best.
- **Status: FIXED (2026-08-20).** Microsecond accumulation landed earlier with the 502/504 work (commit 30d1ca8); this change completes the item. `recordResponseTime` now also maintains a moving average (EWMA, weight 0.2 on the newest sample) in microseconds, stored as float bits in a `uint64` and updated with a CAS loop so the hot path stays lock-free ([internal/proxy/proxy.go:110-127](internal/proxy/proxy.go#L110-L127)). `RecentResponseTime()` exposes it in milliseconds and is what `leastResponseTimeNext` compares ([core/least-algorithm/least-algorithm.go:113-137](core/least-algorithm/least-algorithm.go#L113-L137)), so a single slow response decays away instead of deprioritizing a Backend forever, and each server's time is read once per pass instead of twice per comparison. A Backend with no measurement yet (`0`) is explicitly treated as unknown and picked outright — it has to answer once before it can be compared, which is also how a Rejoined Backend gets its first sample. `AvgResponseTime()` keeps its meaning (lifetime average, what `/stats` and Prometheus report) minus the `rt == 0` special case. Selection also stopped touching the shared `lastIndex` (it scans for the true minimum, so the rotation baseline was pointless) — `BenchmarkLeastResponseTimeNext` went 2.46 ns/op → 1.12 ns/op on an M2. The EWMA's CAS loop costs ~7 ns/request uncontended and ~30 ns on top of the plain atomic add when 8 goroutines hammer one Backend's counter, i.e. well under 0.1% of a request that includes a Backend round trip. Regression tests: `TestResponseTimeSubMillisecond` and `TestRecentResponseTimeDecays` in `internal/proxy`, `TestLeastResponseTimeNextPicksLeast` (true minimum, sub-millisecond spread, unmeasured Backend preferred) in `core/least-algorithm`; `mocks.MockProxy` gained a `ResTime` field. Note: the lifetime average's numerator/denominator mismatch is untouched here — that is H5.

<a id="h5"></a>
### H5. Failed requests counted in the denominator but not the numerator — a failing backend becomes a traffic black hole

- **Where:** [internal/proxy/proxy.go:61](internal/proxy/proxy.go#L61) (count at entry), [:93-100](internal/proxy/proxy.go#L93-L100) (time only on success), [:197-205](internal/proxy/proxy.go#L197-L205); consumed by [core/least-algorithm/least-algorithm.go:109-127](core/least-algorithm/least-algorithm.go#L109-L127)
- **Bug:** `totalRequestCount` increments for every request, but `totalResTime` is only added on full success — every failure drags the average *down*; in-flight requests also inflate the denominator early.
- **Failure scenario:** under `least-response-time`, a backend starts refusing connections between health checks. Each fast failure shrinks its average, so it receives an ever-growing share of traffic for up to `health_checker_time` (default 30s) until the checker removes it.
- **Fix:** record elapsed time on every path (including errors), or track successes in a separate counter used as the denominator.
- **Status: FIXED (2026-08-20).** Re-verified before fixing — the finding was half stale and its suggested fix was rejected. Since H4, selection compares `RecentResponseTime()` (the EWMA), which failures never touched: they no longer *shrink* the score, they *freeze* it. The black hole survived in that shape — a Backend that starts refusing connections keeps its last healthy EWMA (usually the pool's best, since min-takes-all had been feeding it) and receives 100% of traffic until the next Probe round; a still-unmeasured Backend (score 0) failing every request kept winning outright indefinitely via the 0-means-unknown rule. The suggested "record elapsed time on every path" is now actively harmful: a refused connection completes in microseconds, so the failing Backend would score as the *fastest*. Landed, deliberately minimal (a staleness-window variant was built first and then removed as over-engineered — it changed steady-state selection and added a second tuning constant to cover a rare recovery path): **(1)** a failed proxy attempt feeds the EWMA `max(elapsed, failureResponseTimePenalty)` (10s) ([internal/proxy/proxy.go:105-108](internal/proxy/proxy.go#L105-L108), [:138-142](internal/proxy/proxy.go#L138-L142)) — one failure lifts a millisecond-scale score to seconds and traffic shifts away immediately, while a timeout keeps its real (larger) elapsed time. **(2)** A Rejoining Backend's score is reset to unmeasured ([core/least-algorithm/least-algorithm.go:174-176](core/least-algorithm/least-algorithm.go#L174-L176), [internal/proxy/proxy.go:296-301](internal/proxy/proxy.go#L296-L301)), so the 0-wins-outright rule re-measures it with one request and an old penalty cannot starve it after recovery. **(3)** `AvgResponseTime()` divides by a new `measuredRequestCount` incremented together with the numerator on success only ([:275-287](internal/proxy/proxy.go#L275-L287)), so `/stats`/Prometheus report the average over successful requests, no longer dragged down by failures or inflated-early by in-flight requests; `totalRequestCount` keeps its meaning for `TotalReqCount`/`$incremental`. Accepted residual: a Backend whose health endpoint stays green while its requests fail stays penalized until it actually goes Down and Rejoins — the safe direction (capacity loss, not client errors), and a health-endpoint problem more than a Balancer one. Regression tests: `TestFailedRequestPenalizesRecentResponseTime`, `TestFailurePenaltyOutweighsHealthyHistory`, `TestResetRecentResponseTime`, `TestAvgResponseTimeExcludesFailures` in `internal/proxy`; `TestRejoinResetsResponseTimeScore` in `core/least-algorithm`. Cost, A/B-measured against the pre-H5 tree (M2, 8 threads; benchmarks kept in `internal/proxy/proxy_bench_test.go`): recording a success went 11.9→13.2 ns/op serial and 60→92 ns/op under worst-case 8-goroutine contention on a single Backend's counters (the added `measuredRequestCount` increment); the selection-path read is unchanged at 0.3 ns/op; the end-to-end handler benchmark (~28µs/request, dominated by the Backend round trip) shows no delta above run-to-run noise. The penalty path itself runs only on failed requests.

<a id="h6"></a>
### H6. Protocol stripping leaves path/trailing slash in the backend URL → invalid `HostClient.Addr`, every proxied request fails

- **Where:** [pkg/config/config.go:47](pkg/config/config.go#L47) (`protocolRegex`), [:204](pkg/config/config.go#L204); consumed at [internal/proxy/proxy.go:218](internal/proxy/proxy.go#L218) (`Addr: backend.Url`)
- **Bug:** only the scheme prefix is stripped; a path, trailing slash, query, or userinfo survives into `b.Url`, which is used verbatim as `fasthttp.HostClient.Addr` (must be a dialable `host:port`). Nothing validates it.
- **Failure scenario:** `url: http://localhost:8080/` (very common) yields `Addr = "localhost:8080/"`; dialing fails on port `"8080/"` so every proxied request returns 500 — while the health check (`http://localhost:8080//`) can still return 200 on slash-merging servers (nginx), so the backend is happily kept in rotation.
- **Fix:** `url.Parse` the backend URL and extract only `Host`, erroring on anything with a path/query or malformed `host:port`.
- **Status: FIXED (2026-08-22).** `protocolRegex` replaced by `normalizeBackendAddress` ([pkg/config/config.go](pkg/config/config.go)): `url.Parse`-based, producing a dialable **Backend address** (new CONTEXT.md term). An optional `http://` scheme and a bare trailing slash are accepted and stripped; a missing port is normalized to `:80` explicitly (previously implicit via fasthttp); startup errors on a real path, query, fragment, userinfo, malformed `host:port`, or empty url — so L10 is fixed as a side effect. `https://` is rejected with an error naming the TLS-termination model — M6's rejection arm, recorded as [docs/adr/0004-plaintext-only-backends.md](docs/adr/0004-plaintext-only-backends.md). All example configs already pass the new validation. Regression tests: `TestNormalizeBackendAddress` (accept/normalize/reject table) in `pkg/config`.

<a id="h7"></a>
### H7. Logger build error swallowed → nil logger passed to `zap.ReplaceGlobals` → startup panic

- **Where:** [pkg/logger/logger.go:29-30](pkg/logger/logger.go#L29-L30) (`logger, _ := config.Build()`), fed by [pkg/helper/helper.go:83-92](pkg/helper/helper.go#L83-L92)
- **Bug:** if `config.Build()` fails (log sink can't be opened), the error is discarded and nil goes into `zap.ReplaceGlobals`, which immediately calls `logger.Sugar()` → nil-pointer panic (verified in zap v1.27.1). `CreateLogDirIfNotExist` makes this reachable: a permission error from `os.Stat` is not `ErrNotExist`, so it returns nil and the unwritable path is used instead of falling back to `./divisor.log`.
- **Failure scenario:** on Linux, `/var/log/divisor/` exists from an earlier root run; the process now runs as non-root. `Build` fails to open the sink and `main.go:32` panics with a nil dereference before any usable error message.
- **Fix:** check the `Build` error and fall back to a stdout-only config; treat any stat error other than "not exist" as failure in `CreateLogDirIfNotExist`.
- **Status: FIXED (2026-08-22).** Done as a cascade so the process always starts: the log-path helpers moved from `pkg/helper` into `pkg/logger` (S4, now unexported in [pkg/logger/logfile.go](pkg/logger/logfile.go)); `createLogDirIfNotExist` surfaces *any* stat error (not just `ErrNotExist`) so an unusable dir falls back to `./divisor.log`; the Windows `LocalAppData` empty-check now runs before concatenation (L11 fixed — extracted as testable `logFolderFor(goos, localAppData)`); and `InitLogger` checks `Build`'s error, rebuilding with stdout-only sinks plus a warning naming the unopenable file (`zap.NewNop` as the last-resort guarantee that `ReplaceGlobals` never sees nil). Regression tests in `pkg/logger`: `TestInitLoggerFallsBackToStdoutOnUnopenableSink` (panicked on old code), `TestInitLoggerWritesFile`, `TestLogFolderFor`, `TestCreateLogDirIfNotExist` (incl. permission-denied stat).

<a id="h8"></a>
### H8. net/http adapter forwards stale Content-Length — middleware body rewrites get truncated or hang

- **Where:** [internal/proxy/nethttp_adapter.go:56-65](internal/proxy/nethttp_adapter.go#L56-L65)
- **Bug:** the adapter's header copy loop forwards the backend's parsed `Content-Length` into `w.Header()`, but fasthttp's `Response.SetBody/SetBodyString` (what an `OnResponse` middleware uses) does **not** update the stored content length — only `Response.Write` recomputes it, which the adapter path never calls.
- **Failure scenario:** http2 mode + a middleware rewriting a response body: backend returns `"hello"` (CL=5), middleware sets `"goodbye world"` (13 bytes). The adapter sends `Content-Length: 5`; net/http truncates at 5 bytes and returns `http.ErrContentLength` (silently ignored). The client receives `"goodb"`. A shorter rewrite ends the stream early — client errors/hangs.
- **Fix:** skip `Content-Length` (and `Trailer`) in the header copy loop and let net/http derive it from the bytes written, or set it from `len(ctx.Response.Body())`.
- **Status: FIXED (2026-08-22).** The header-copy loop skips `Content-Length` ([internal/proxy/nethttp_adapter.go](internal/proxy/nethttp_adapter.go)); net/http derives framing from the bytes actually written — chosen over setting it from `len(Body())` so the path stays correct if response streaming (L8) ever lands. `Trailer` needed nothing: it was already in `hopHeaders`. Regression tests: `TestNetHttpAdapterIgnoresStaleContentLength` in `internal/proxy` (real `httptest.NewServer`, since the recorder doesn't enforce Content-Length; verified failing on the pre-fix code — the shorter rewrite dies with `unexpected EOF`), plus integration Scenario `TestHTTP2MiddlewareBodyRewrite` (HTTP/2 + a middleware rewriting the body shorter and much longer, full body asserted end to end).

---

## Medium

<a id="m1"></a>
### M1. least-connection picks the first improvement, not the least-connected server

- **Where:** [core/least-algorithm/least-algorithm.go:89-107](core/least-algorithm/least-algorithm.go#L89-L107)
- **Bug:** the loop `break`s at the first server with fewer pending requests than the baseline, instead of scanning for the minimum. (`leastResponseTimeNext` right below correctly omits the `break`, confirming it's unintended.)
- **Failure scenario:** 3 backends with pending [5, 3, 1], `lastIndex=0`: picks the 3-pending server, never considers the 1-pending one. The truly least-loaded backend is systematically under-utilized. Existing tests use only 2 backends, masking it.
- **Fix:** remove the `break`.

<a id="m2"></a>
### M2. `isHostAlive` read by `Stats()` while written by the health checker — data race

- **Where:** [core/round-robin/round-robin.go:134](core/round-robin/round-robin.go#L134) vs [:107](core/round-robin/round-robin.go#L107)/[:119](core/round-robin/round-robin.go#L119); same in all five balancers
- **Bug:** the monitoring goroutine calls `Stats()` every 5s and on `/stats` requests, reading `serverMap.isHostAlive` unsynchronized against health-checker writes.
- **Failure scenario:** monitoring scrape concurrent with a health-state flip: torn/stale liveness reporting; any future field on `serverMap` inherits the race.
- **Fix:** fold into the mutex from C2, or make `isHostAlive` an `atomic.Bool`.

<a id="m3"></a>
### M3. `panic("All backends are down")` irrecoverably kills the proxy *(judgement call — deliberate but harsh)*

- **Where:** [core/round-robin/round-robin.go:99-101](core/round-robin/round-robin.go#L99-L101); same in all five balancers
- **Bug:** when the last live backend fails a health check, the checker goroutine panics and crashes the process — the code's own recovery mechanism (re-add next cycle) can never run.
- **Failure scenario:** all backends share one database that restarts for 60s → every backend fails one health round → divisor panics and stays dead after the backends recover, converting a transient outage into a permanent one. In `random`, a request racing the `len--` can also hit `rand.Uint32N(0)`, which panics.
- **Fix:** keep serving 502/503 while the pool is empty and let the health checker re-add backends when they return.
- **Status: FIXED (2026-08-18, commit b733a1a).** The health checker no longer panics — it logs a warning and keeps running ([core/round-robin/round-robin.go:110-112](core/round-robin/round-robin.go#L110-L112)); requests hitting an empty pool get 503 via `proxy.NoAliveBackends` ([internal/proxy/proxy.go:120-127](internal/proxy/proxy.go#L120-L127)). `random`'s `rand.IntN(0)` panic case is gone too — every `next()` nil-guards an empty snapshot. Covered by the integration suite's all-backends-down scenario.

<a id="m4"></a>
### M4. Virtual-node keys `string(rune(Id+i)) + Addr` collide for duplicate backend URLs — ring corruption and request-path panic

*Cross-confirmed by two independent reviewers.*

- **Where:** [pkg/consistent/consistent.go:60](pkg/consistent/consistent.go#L60) and [:72](pkg/consistent/consistent.go#L72); node Ids assigned at [core/ip-hash/ip-hash.go:49](core/ip-hash/ip-hash.go#L49)
- **Bug:** nodes with the same `Addr` generate overlapping rune ranges (Id=0,i=1 collides with Id=1,i=0), so `AddNode` overwrites map entries while `numbers` accumulates duplicate hashes; removing one node strips the shared hashes the other node still relies on.
- **Failure scenario:** the same backend URL listed twice in config (accepted without complaint; a normal way to double a backend's share in other algorithms). When one duplicate flaps down, `RemoveNode` strips the shared hashes from both map and ring, silently removing the still-healthy twin's positions too. Even without removal, distribution is silently skewed. (Post-C3 the old nil-interface panic is structurally impossible — the damage is now silent misrouting/starvation rather than a crash.)
- **Fix:** collision-free key, e.g. `fmt.Sprintf("%d|%d|%s", node.Id, i, node.Addr)`; optionally reject duplicate backend URLs in `PrepareConfig`.

<a id="m5"></a>
### M5. `OnResponse` cannot override backend errors as documented — `serverError` clobbers the custom response and leaks the dial error

- **Where:** [internal/proxy/proxy.go:86-97](internal/proxy/proxy.go#L86-L97), [:153-164](internal/proxy/proxy.go#L153-L164); contract in README ("OnResponse can override backend errors")
- **Bug:** when `proxy.Do` fails and `OnResponse` writes a fallback response then returns nil (the intuitive "handled" signal), `serverError()` unconditionally overwrites status to 502/504, sets `Connection: close`, and replaces the body with the raw dial error. The only way to preserve a crafted response is returning a non-nil error — which is then dropped (H3).
- **Failure scenario:** middleware serves a cached page on backend failure, returns nil → client gets `{"message":"dial tcp …: connection refused"}` with 502 instead of the fallback (also leaking internals; see L3).
- **Fix:** give the contract an explicit "handled" signal (sentinel error or response-modified check) and skip `serverError` when it's set.

<a id="m6"></a>
### M6. `https://` backend URLs silently accepted, then treated as plain HTTP everywhere

- **Where:** [pkg/config/config.go:47](pkg/config/config.go#L47)+[:204](pkg/config/config.go#L204) (strips `https://` like `http://`), [:70-72](pkg/config/config.go#L70-L72) (health URL hardcodes `http://`); [internal/proxy/proxy.go:108](internal/proxy/proxy.go#L108), [:217-228](internal/proxy/proxy.go#L217-L228) (no `IsTLS`)
- **Bug:** a TLS-only backend can never work, but the config layer drops the scheme without error.
- **Failure scenario:** `url: https://api.example.com` → health check GETs the plain-HTTP URL → 301 redirect → treated as down → backend never added; with all backends https, startup fails with the unrelated-looking "No available servers". If the health endpoint answers 200 over HTTP, traffic is proxied **unencrypted** to port 80.
- **Fix:** either reject `https://` URLs with a clear error, or preserve the scheme, set `HostClient.IsTLS`, and use an https health-check URL.
- **Status: FIXED (2026-08-22), with H6.** The reject arm was chosen: `normalizeBackendAddress` errors at startup on `https://` with a message naming the TLS-termination model. Decision recorded in [docs/adr/0004-plaintext-only-backends.md](docs/adr/0004-plaintext-only-backends.md) (TLS-to-backend deliberately deferred as an additive post-1.0 feature).

<a id="m7"></a>
### M7. Typed-nil `any` comparison defeats the server-startup failure check

- **Where:** [main.go:78-89](main.go#L78-L89), [:155-160](main.go#L155-L160) (found tracing `pkg/config`'s Http2 constant; kept here because the fix belongs with config/server wiring)
- **Bug:** `startNetHttpServer` returns a typed nil `*http.Server` when `http2.ConfigureServer` fails; stored in an `any`, the interface is non-nil, so `if server == nil` never fires.
- **Failure scenario:** http2 configuration error → process runs indefinitely with no HTTP listener; later `performGracefulShutdown` calls `Shutdown` on the nil `*http.Server` and panics.
- **Fix:** return `(server, error)` from both start functions and check the error.

<a id="m8"></a>
### M8. Headers nominated in the `Connection` header are not removed (RFC 7230 §6.1)

- **Where:** [internal/proxy/proxy.go:103-118](internal/proxy/proxy.go#L103-L118), [internal/proxy/nethttp_adapter.go:56-61](internal/proxy/nethttp_adapter.go#L56-L61)
- **Bug:** only the fixed `hopHeaders` list is deleted; the `Connection` header's *value* is never parsed to remove the headers it nominates (the comment at proxy.go:30-34 acknowledges the requirement; Go's `httputil.ReverseProxy` does both).
- **Failure scenario:** backend replies `Connection: X-Internal-Debug` + `X-Internal-Debug: <internal data>`; the internal header is forwarded to the client. Inbound, a client's `Connection: X-Secret` makes `X-Secret` reach the backend as if end-to-end.
- **Fix:** before deleting `Connection`, split its value on commas and delete each nominated header on both request and response paths.

<a id="m9"></a>
### M9. `$incremental` header is not atomic — concurrent requests get duplicate values

- **Where:** [internal/proxy/proxy.go:61](internal/proxy/proxy.go#L61) and [:173-174](internal/proxy/proxy.go#L173-L174)
- **Bug:** `atomic.AddUint64` at handler entry discards the return value; the counter is re-read later with `atomic.LoadUint64` — a read-after-add race.
- **Failure scenario:** request A: Add→1; request B: Add→2; both Load→2. Both backend requests carry the value 2 and 1 never appears — duplicated/skipped sequence numbers break request correlation.
- **Fix:** capture `v := atomic.AddUint64(…)` at entry and use `v` in `setCustomHeaders`.

---

<a id="low"></a>
## Low — bugs & sharp edges

### L1. `X-Forwarded-For` overwrites any existing chain *(judgement call)*
[internal/proxy/proxy.go:110](internal/proxy/proxy.go#L110) — an incoming XFF from an upstream proxy/CDN is replaced, not appended; behind another proxy the real client IP is lost. Overwriting is a defensible anti-spoofing default. **Fix:** append (`prior + ", " + clientIP`) or make it configurable.

### L2. `$time` stamps local time with a literal `Z` (UTC) suffix
[internal/proxy/proxy.go:172](internal/proxy/proxy.go#L172) — `time.Now().Local().Format("2006-01-02T15:04:05.000Z")`: `Z` is a literal in that layout, so a UTC+3 host claims `12:00Z` when UTC is 09:00. **Fix:** `time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")`.

### L3. `serverError` builds JSON by concatenation without escaping, and leaks internal error detail
[internal/proxy/proxy.go:153-164](internal/proxy/proxy.go#L153-L164) — invalid JSON when the error contains `"` or `\`; exposes backend addresses/dial errors to clients. **Fix:** `json.Marshal` a generic message; log detail server-side.

### L4. Monitoring stats goroutine can never be stopped, and starts before the listener bind succeeds
[internal/monitoring/monitoring.go:102-108](internal/monitoring/monitoring.go#L102-L108), [:137-141](internal/monitoring/monitoring.go#L137-L141) — no stop channel; if `reuseport.Listen` fails it polls gopsutil + `Stats()` forever; keeps touching balancer state during/after graceful shutdown. **Fix:** `time.Ticker` + done channel wired into shutdown; start only after `Listen` succeeds.

### L5. Transient gopsutil error zeroes all global Prometheus gauges
[internal/monitoring/monitoring.go:50-77](internal/monitoring/monitoring.go#L50-L77), [internal/monitoring/prometheus.go:69-77](internal/monitoring/prometheus.go#L69-L77) — one failed collection publishes a zero-valued snapshot (CPU/mem/goroutines → 0), firing false alerts. **Fix:** skip the update on error, keep last values.

### L6. net/http adapter ignores client cancellation *(judgement call)*
[internal/proxy/nethttp_adapter.go:27-66](internal/proxy/nethttp_adapter.go#L27-L66) — `r.Context()` never consulted; a disconnected HTTP/2 client leaves the backend request running, holding a connection. **Fix:** watch `r.Context().Done()` with `DoDeadline`/abort, or document the limitation.

### L7. net/http adapter drops client-sent trailer values
[internal/proxy/nethttp_adapter.go:122-129](internal/proxy/nethttp_adapter.go#L122-L129) — `r.Trailer` is read before the body is consumed (net/http populates values only after full body read), so actual trailer values are never forwarded. **Fix:** forward trailers after body consumption, or drop trailer support explicitly.

### L8. Streaming responses (SSE/long-poll) are fully buffered and never reach the client
[internal/proxy/proxy.go:78-84](internal/proxy/proxy.go#L78-L84) (`HostClient.Do`/`DoTimeout` reads the whole response; no `StreamResponseBody`), [internal/proxy/nethttp_adapter.go:64-65](internal/proxy/nethttp_adapter.go#L64-L65) (no `http.Flusher` path) — an SSE backend never terminates its body, so `Do` blocks indefinitely and nothing is flushed. **Fix:** enable `StreamResponseBody` and stream/flush in both paths, or document streaming as unsupported.

### L9. `GetNode` has no empty-ring guard — ✅ fixed with C3
[pkg/consistent/consistent.go:90-102](pkg/consistent/consistent.go#L90-L102) — `GetNode` now returns nil on an empty ring, and ip-hash serves 503 via `proxy.NoAliveBackends` ([core/ip-hash/ip-hash.go:82-88](core/ip-hash/ip-hash.go#L82-L88)). Regression test `TestGetNodeEmptyRing`.

### L10. Empty backend `url` passes validation — ✅ fixed with H6
`normalizeBackendAddress` rejects an empty (or whitespace-only) `url` at startup; the package tests that constructed `Backend{}` now use real addresses.

### L11. `GetLogFolder` Windows fallback is dead code — ✅ fixed with H7
Now `logFolderFor(goos, localAppData)` in [pkg/logger/logfile.go](pkg/logger/logfile.go): the env var is checked *before* concatenating, so an unset `LocalAppData` falls back to `./divisor.log` instead of the drive root. Covered by `TestLogFolderFor` (testable cross-platform).

### L12. `tcp_keepalive_period` config option is a silent no-op
[pkg/config/config.go:84](pkg/config/config.go#L84), wired at [main.go:109](main.go#L109) — fasthttp applies the period only when `Server.TCPKeepalive` is true, which divisor never sets. **Fix:** set `TCPKeepalive: true` when a period is configured.

### L13. Unknown `http_version` values silently coerced to `http1.1`
[pkg/config/config.go:241-243](pkg/config/config.go#L241-L243) — any typo (`HTTP2`, `h2`, `http/2`) silently becomes http1.1; the user believes HTTP/2 is active. **Fix:** error on values other than `""`, `"http1.1"`, `"http2"`.

### L14. Health check accepts only exactly HTTP 200
[pkg/http/http.go:39](pkg/http/http.go#L39) — backends answering 204/301/302 on their health path are permanently down (fasthttp doesn't follow redirects), not configurable. **Fix:** accept 2xx or make the expected status configurable.

### L15. Yaegi `Eval` failure masked by `ErrNewFunctionNotFound`
[pkg/middleware/executor.go:93-96](pkg/middleware/executor.go#L93-L96) — any `Eval` error is replaced wholesale, discarding the underlying cause; operators debug the wrong thing. **Fix:** wrap: `fmt.Errorf("%w: %v", ErrNewFunctionNotFound, err)`.

### L16. Dead middleware validation error variables
[pkg/middleware/executor.go:20-26](pkg/middleware/executor.go#L20-L26) — the `ErrOnRequestFunctionNotFound/NotValid` and `ErrOnResponseFunctionNotFound/NotValid` variables are declared, never referenced (`ErrNewFunctionNotValid` is used now); they advertise checks that don't exist. **Fix:** delete or implement.

### L17. Duplicated middleware validation with divergent error values
[pkg/middleware/executor.go:54-60](pkg/middleware/executor.go#L54-L60) vs [pkg/config/config.go:277-292](pkg/config/config.go#L277-L292) — same empty/both-set checks in two places with different error values; the executor's copy is unreachable in the production startup path. **Fix:** validate in one place.

### L18. Middleware contract exposes the pooled `RequestCtx` with no retention warning
[middleware/middleware.go:7-18](middleware/middleware.go#L7-L18) — the contract's `Context` embeds `*fasthttp.RequestCtx`, and fasthttp recycles `RequestCtx`; a middleware that captures it in a goroutine reads a recycled ctx serving a different request — cross-request data leakage. **Fix:** document the no-retention rule in the interface and README.

### L19. `OnResponse` chain runs in registration order, not reverse (onion) order *(judgement call — undocumented)*
[pkg/middleware/executor.go:134-141](pkg/middleware/executor.go#L134-L141) — outer middleware never observes inner middleware's response mutations. **Fix:** iterate in reverse for `RunOnResponse` and document the ordering.

### L20. Middleware instances are shared across all request goroutines — undocumented
[pkg/middleware/executor.go:106](pkg/middleware/executor.go#L106), [internal/proxy/proxy.go:71-91](internal/proxy/proxy.go#L71-L91) — the per-middleware yaegi setup avoids the classic concurrent-Eval hazard (same pattern as Traefik plugins), but each middleware is a single shared instance: any mutable field in a user's struct is raced by concurrent requests with no warning in the contract. **Fix:** document that stateful middleware must synchronize internally.

---

<a id="smells"></a>
## Code smells (judgement calls)

### S1. Duplicated Code / Shotgun Surgery: five near-identical balancer implementations
`core/round-robin`, `core/w-round-robin`, `core/random`, `core/least-algorithm`, `core/ip-hash` — `serverMap`, constructor loop, `healthChecker`, `healthCheck`, `Stats()`, and `Shutdown()` are near-verbatim copies (the `healthChecker` loop is byte-identical apart from the receiver). Concrete cost: **C1, C2, H1, H2, and M3 each required the same fix in five files (already paid); M2 still does.** Extract a shared base type embedding servers/serversMap/health-checking/Stats/Shutdown, with algorithms supplying only `next()` — ideally *before* fixing the remaining one, so each fix lands once.

### S2. Mysterious Names: `len`, `i`, `lastIndex` *(partially addressed by the C1/C2 fixes)*
`serverMap.i` is now `statsIdx` and the slice-based balancers' separate `len` fields are gone (length comes from the snapshot). Remaining: [core/ip-hash/ip-hash.go:30](core/ip-hash/ip-hash.go#L30) still has `len` (count of alive nodes), the rotation counter `i` at [core/round-robin/round-robin.go:29](core/round-robin/round-robin.go#L29)/[core/w-round-robin/w-round-robin.go:31](core/w-round-robin/w-round-robin.go#L31), and [core/least-algorithm/least-algorithm.go:30](core/least-algorithm/least-algorithm.go#L30) `lastIndex` (actually stores "index of the current least server"). These names actively obscured C1 and M1; rename to `aliveCount`, `counter`, `leastIndex`.

### S3. `FindIndex` returns `(0, err)` on miss — a valid-index sentinel
[pkg/helper/helper.go:48-56](pkg/helper/helper.go#L48-L56) — index 0 is a legitimate result, so any caller dropping the error deletes element 0. The sole current caller checks, but the API invites the bug. Return `-1` or `(int, bool)`.

### S4. Log-path helpers misplaced in `pkg/helper` (low cohesion) — ✅ addressed with H7
Moved to [pkg/logger/logfile.go](pkg/logger/logfile.go); only `GetLogFile` stays exported (main.go's sole need), the rest are unexported.

### S5. Repeated type switches on `server any`
[internal/monitoring/monitoring.go:45,84-91](internal/monitoring/monitoring.go#L84-L91) and [main.go:182-193](main.go#L182-L193) — parallel `case *fasthttp.Server / case *http.Server` switches in every consumer; a small interface (`OpenConnectionsCounter`/`Shutdowner`) removes both and fixes the M7 typed-nil trap structurally.

### S6. net/http adapter re-derives the handler closure on every request
[internal/proxy/nethttp_adapter.go:54](internal/proxy/nethttp_adapter.go#L54) — `a.Balancer.Serve()(&ctx)` constructs a new closure per request; every `Serve()` is a pure factory that main.go calls once and reuses. Cache `a.handler = balancer.Serve()` in the constructor.

---

## Suggested fix order

1. **S1 (extract shared balancer base)** — C1/C2/H1/H2/M3 already landed five times over without it; still worth doing before the next step so M2 lands once.
2. **M2** — remaining shared-base fix: atomic/mutexed `isHostAlive`. *(C1, C2, H1, H2, M3 done.)*
3. **M4** — collision-free vnode keys in `pkg/consistent`. *(C3 and L9 done.)*
4. **H3 + M5** — middleware error-to-response translation + explicit "handled" signal. *(C4's per-request `recover()` done.)*
5. **M1 + M9** — remaining metrics/selection correctness in `internal/proxy` + `least-algorithm`. *(H4 and H5 done.)*
6. **M7** — remaining config/startup robustness. *(H6, H7, M6 done.)*
7. **The rest** — low-severity items, batched as convenient. *(H8 done.)*
