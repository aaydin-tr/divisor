# TODO — 1.0 backlog

Working list for the 1.0 release (breaking changes allowed). Items marked
**[born-red]** have an integration test in `integration-test/` that already
asserts the target behavior and stays red until it ships.

## Config surface additions

New `server`/backend options to expose in `pkg/config` (today these are either
hardcoded or silently left at library defaults):

- [ ] `server.max_request_body_size` — exposed in `pkg/config` (default 4MB),
  applied on both paths (fasthttp `MaxRequestBodySize` + custom `ErrorHandler`
  for the 413; `nethttp_adapter.go` rejects declared Content-Length over the
  cap up front and buffers length-less bodies up to the cap so an oversized
  payload never reaches a backend), returns **413 Request Entity Too Large**
  when exceeded. **[born-red:** `TestProxyMatrix/BodyOverLimit413`**]**
- [ ] Upstream/proxy timeout (e.g. `backends[].proxy_timeout` or a global
  `server.proxy_timeout`) — `internal/proxy` calls `Do` with no deadline, so a
  hanging Backend hangs the client request forever (`read_timeout`/`write_timeout`
  do not cover time spent inside the handler). Switch to `DoDeadline`/`DoTimeout`
  and surface expiry as 504. **[born-red:** `TestPausedBackendBoundedFailure`**]**
- [ ] `server.read_buffer_size` / `server.write_buffer_size` — fasthttp's 4KB
  default caps request header size; large cookies/JWTs hit "431/400 header too
  large" with no way to raise it.
- [ ] `server.max_conns_per_ip`, `server.max_requests_per_conn` — basic
  self-protection knobs, currently unavailable.
- [ ] `server.graceful_shutdown_timeout` — hardcoded to 30s in
  `main.go` (`performGracefulShutdown`).
- [ ] HTTP/2 server tuning (existing `TODO` at `main.go:158`) — pass a
  configured `http2.Server` (e.g. `max_concurrent_streams`, `idle_timeout`)
  instead of the zero value.
- [ ] Health Probe tuning — probe timeout and expected-status are hardcoded
  (GET, only 200 counts as Alive, client defaults in `pkg/http`); consider
  `health_checker_timeout` and per-backend expected status.
- [ ] TLS tuning — `tls_min_version` (and optionally cipher suites); currently
  whatever crypto/tls defaults to.

## 1.0 behavior fixes (agreed spec, tests already red)

- [x] A Backend that is Down at startup must Rejoin once its Probe succeeds —
  fixed: all five algorithms now register every backend in their server map at
  startup (down ones with `isHostAlive: false`, kept out of rotation until
  their Probe succeeds). **[born-red:** `TestBackendDownAtStartupCanRejoin`
  **— now green]**
- [x] Unreachable Backend → **502 Bad Gateway**, not 500 — fixed:
  `serverError` in `internal/proxy/proxy.go` now sets `StatusBadGateway`;
  applies to both the fasthttp and HTTP/2 paths since the status is set on
  the shared `RequestCtx`. **[born-red:** `TestUnreachableBackendGets502`,
  `TestProxyMatrix/FailedProxyAttemptReturns502` **— now green]**
- [x] All Backends Down → stay up, serve 502/503, let Backends Rejoin — fixed:
  the health checker no longer panics when the last live backend leaves the
  rotation (all five algorithms); requests hitting an empty rotation get
  **503 Service Unavailable** (`proxy.NoAliveBackends`) until a Probe lets a
  backend Rejoin. **[born-red:** `TestAllBackendsDownStaysUp` **— now green]**
- [ ] Zero Alive Backends at boot → start anyway, serve 503, let Backends
  Rejoin (remove the "No available servers" bail-out). Today every balancer
  constructor returns nil when no Backend is Alive at startup and `main.go`
  logs "No available servers" and exits (`main.go:67-70`) — with exit code 0.
  That guard protects nothing else: `PrepareConfig` already rejects an empty
  backend list (`ErrAtLeastOneBackend`) and an invalid `type`, so the only
  reachable case is "all configured Backends failed their first Probe" —
  exactly the outage the shipped all-Backends-Down item survives at runtime.
  Asymmetry to fix: all Backends Down 1s after boot → stay up + 503 + Rejoin;
  1s before boot → refuse to start. Orchestrated deploys (compose/k8s) often
  start the LB before the Backends; nginx/HAProxy/Envoy all boot regardless of
  upstream health. The machinery already exists: Down Backends are registered
  in `serversMap` at startup and the empty-rotation 503 path
  (`proxy.NoAliveBackends`) is shipped and tested.
  Implementation notes:
  - Each of the 5 constructors must `servers.Store(&servers)` **before** the
    `len(servers) == 0` check it currently bails on (today it returns nil
    before storing; removing the check naively nil-derefs in `next()`), then
    start the health checker and return the balancer. ip-hash: drop the
    `ipHash.len <= 0` bail (the consistent-hash ring handles empty).
  - Keep `main.go`'s nil check as a defensive backstop — already exits
    non-zero since the Dockerfile/entrypoint exit-code item below shipped.
  - Unit tests to flip: constructor tests asserting nil for
    `mocks.TestCases[2]` (all Backends down, `ExpectedServerCount: 0`) now
    expect a live balancer serving 503; `TestCases[3]` (empty backend list)
    stays nil — that case can't pass `PrepareConfig` anyway.
  - Do it born-red: integration scenario with every Backend `StartDown: true`
    → divisor boots, serves 503, then one Probe succeeds → traffic flows.
  - Trade-off accepted: a typo'd backend URL no longer fails fast at boot —
    divisor serves 503 instead of exiting; per-backend liveness stays visible
    in `/stats`, and this raises the value of the monitoring readiness-probe
    item under "Suite / CI follow-ups".
- [ ] Decide X-Forwarded-For semantics: current overwrite behavior is pinned by
  `TestProxyMatrix/XForwardedFor` as anti-spoofing; revisit whether 1.0 should
  append instead.
- [x] Dockerfile/entrypoint: divisor exits **0** when the config file is
  missing or invalid — fixed: every startup-failure path in `main.go` (empty
  flag, missing file, parse error, `PrepareConfig` error, middleware error,
  nil balancer, listen/server-start error) now uses `zap.S().Fatal*`, which
  exits 1 after syncing the log. **[born-red:**
  `TestConfigErrorExitsNonZero` **— now green]**

## Suite / CI follow-ups

- [x] Docker layer caching for the Integration job — CI now builds both suite
  images with buildx + `type=gha` cache (`.github/workflows/integration.yml`)
  and sets `DIVISOR_IT_PREBUILT=1` so `TestMain` skips its own `docker build`
  (it verifies the images exist instead). Pushes to `main` also run the suite
  (`go.yml`) to seed the base-branch cache, since PR-scoped caches are not
  shared across PRs. Local runs are unchanged.
- [x] `t.Parallel` across scenarios — every top-level test calls
  `t.Parallel()`; scenarios were already isolated by container/network names.
- [x] Born-red gating decided (see `docs/adr/0002-spec-red-gating.md`):
  spec-red tests call `specRed(t, ...)` and skip unless
  `DIVISOR_INTEGRATION_SPEC_RED=1`; the blocking Integration job stays green
  while an advisory `continue-on-error` job runs just the spec-red set
  (currently `TestPausedBackendBoundedFailure`,
  `TestProxyMatrix/BodyOverLimit413`). Shipping a spec item = remove its
  `specRed` call and drop it from the advisory job's `-run` pattern.
- [ ] Monitoring server coverage (explicitly out of scope for the first suite);
  needs a readiness probe that doesn't conflate divisor liveness with Backend
  health before `/metrics` can be tested with zero Alive Backends.
