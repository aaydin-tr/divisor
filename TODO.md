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
  **503 Service Unavailable** (`types.NoAliveBackends`) until a Probe lets a
  backend Rejoin. **[born-red:** `TestAllBackendsDownStaysUp` **— now green]**
- [ ] Decide X-Forwarded-For semantics: current overwrite behavior is pinned by
  `TestProxyMatrix/XForwardedFor` as anti-spoofing; revisit whether 1.0 should
  append instead.
- [ ] Dockerfile/entrypoint: divisor exits **0** when the config file is
  missing or invalid — should exit non-zero so orchestrators notice.

## Suite / CI follow-ups

- [ ] Docker layer caching for the Integration job (~2–4 min/run savings).
- [ ] `t.Parallel` across scenarios (each already has isolated containers/network names).
- [ ] Decide born-red gating: the Integration job stays red on every PR until
  the spec items above ship; option is a second env var to skip spec-red tests
  in PR CI and run them in a separate advisory job.
- [ ] Monitoring server coverage (explicitly out of scope for the first suite);
  needs a readiness probe that doesn't conflate divisor liveness with Backend
  health before `/metrics` can be tested with zero Alive Backends.
