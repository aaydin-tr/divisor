# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Build
```bash
go build -o divisor
```

### Regenerate yaegi symbols
```bash
# After any change to the middleware/ package (the author-facing contract):
# rewrites pkg/middleware/middleware_symbols.go
go generate ./pkg/middleware/
```

### Run
```bash
# Default config (config.yaml in current directory)
./divisor

# With specific config file (use absolute path)
./divisor --config /absolute/path/to/config.yaml
```

### Testing
```bash
# Run all tests (CI runs them with -race; data races are build failures)
go test -race ./...

# Run tests with verbose output
go test -v ./...

# Run tests for specific package
go test ./core/pool
go test ./core/round-robin
go test ./internal/proxy

# Run single test
go test -run TestFunctionName ./package/path

# Run the black-box integration suite (requires a running Docker daemon;
# opt-in via env var; its own Go module, so plain `go test ./...` never
# touches it)
DIVISOR_INTEGRATION=1 go -C integration-test test -v -timeout 20m .
```

### Install
```bash
go install github.com/aaydin-tr/divisor@latest
```

## Architecture

### Core Load Balancing System

`core/` has two halves (vocabulary in `CONTEXT.md`):

- **Pool** (`core/pool`: `pool.go`, `backend.go`, `rotation.go`) — owns every
  configured `Backend` (Index, address, weight, Probe URL, proxy
  client, Alive flag), runs the Probe loop every `config.HealthCheckerTime`,
  moves Backends between Alive and Down, resets a Rejoining Backend's
  response-time score, builds `Stats()` rows in config order (`BackendHash`
  is the Backend's Index: its config position at boot, a never-used-before
  number for a Backend a Reload added), and closes every proxy on
  `Shutdown()`. `NewPool` Probes once synchronously and does not start the
  loop; `StartHealthChecker()` does. `ProbeAllBackends()` is the one Probe
  round, shared by the loop and by tests, so unit tests never race a
  goroutine. A Reload goes through `SetProbeSettings()` (only when it changes them: it restarts the loop timer) and
  `ReplaceBackends()` (added Backends Probed and Joined, removed ones Leave
  and close, survivors untouched); `transitionsMu` makes Probe rounds and
  Reloads the Balancer's one writer. It never imports a balancer package.
- **Balancers** (one package each: `core/round-robin`, `core/w-round-robin`,
  `core/random`, `core/least-connection`, `core/least-response-time`,
  `core/ip-hash`) — selection only. Each exports `New(cfg)
  pool.Balancer`; `pool.Balancer` is `Join(*Backend)` / `Leave(*Backend)`,
  called by the Pool (single writer) on liveness transitions and Reloads, and
  `Pick(ctx)`, which runs on request goroutines and returns nil when nothing
  is Alive. The slice-based ones embed `pool.Rotation` (copy-on-write Alive
  slice); ip-hash places each Backend on the ring (`pkg/consistent`) by its
  Index on Join, so it accepts Backends it was not constructed with.

`core.NewBalancer(cfg, middlewareExecutor, proxyFunc)` is the only wiring:
it turns `cfg.Backends` into Backends, picks the balancer by `cfg.Type`,
builds the Pool with `cfg.HealthCheckerFunc`, returns nil only if the type is
unknown, starts the loop and returns a `core.LoadBalancer`
(`types.IBalancer`'s `Serve`/`Stats`/`Shutdown` plus `ApplyReload`) — the
Pool plus the balancer as the rest of the process sees them. Zero Alive Backends at boot is a served state (a warning
is logged, requests get 503, Backends Rejoin via Probes), so divisor can
start before its Backends. `Serve` answers 503 (`proxy.NoAliveBackends`)
when `Pick` returns nil.

### Proxy Layer

`internal/proxy` handles the actual HTTP proxying:
- `ProxyClient` wraps `fasthttp.HostClient` for each backend
- Removes hop-by-hop headers (Connection, Keep-Alive, etc.)
- Sets `X-Forwarded-For` header with client IP
- Forwards headers as the client sent them: no default `Content-Type` or `User-Agent`; URI userinfo becomes `Authorization: Basic` in `preReq` (a client Authorization header wins)
- Supports custom headers with special variables: `$remote_addr`, `$time`, `$uuid`, `$incremental`
- Tracks metrics: total requests, average response time, last use time, Pending requests (an open stream counts until it ends)
- Streamed responses (`server.streaming`, ADR 0007) live in `streaming.go`:
  `forwardAllowingStream` applies the streaming rule; a `streamedBody` is
  attached to the response and pumped by the server after
  `ReverseProxyHandler` returns, re-arming the idle deadline before every
  Read through the dialer-wrapped `backendConn`, and the client write
  deadline after it (`boundClientWrites`: fasthttp `ctx.SetWriteDeadline`,
  net/http `ResponseController.SetWriteDeadline`, never past
  `write_timeout`), so a client that stops reading is reaped too. A Read's
  bytes count as forwarded once the stack asks for more, which both stacks
  do only after writing them. `endWith` ends a stream
  once (first cause wins): Pending count, debug `Streamed response ended`
  with its `cause`, and the stream's Access log line. A client watch ends
  it from its own goroutine — `NewFasthttpHandler` (the HTTP/1.1 server's
  Handler) reads `ctx.Conn()`, the net/http adapter uses
  `context.AfterFunc(r.Context())` — so anything `endWith` reads is atomic
  and copied out of ctx

### Configuration System

`pkg/config`:
- YAML-based configuration with validation in `PrepareConfig()`; decoding is strict (`KnownFields`), so an unknown or removed key is a startup error
- Auto-sets defaults if not specified (e.g., `health_checker_time: 30s`, `type: round-robin`)
- Backend `url` is normalized to a dialable Backend address (`host:port`): optional `http://` and bare trailing slash stripped, missing port defaults to 80; path/query/userinfo and `https://` are rejected at startup (ADR 0004: backends are plaintext-only)
- HTTP/2 requires TLS (cert_file + key_file must be provided)
- Weighted round-robin with single backend auto-converts to regular round-robin
- `PlanReload(running, newYAML)` is the pure Reload classifier (ADR 0006): it
  parses and prepares the new config, rejects it as invalid
  (`ErrReloadInvalidConfig`) or for changed restart-only keys
  (`*RestartRequiredError`, every key named, found by reflecting over yaml
  tags), else returns a `ReloadPlan` (kept/added/removed Backends by
  position). Only `backends`, `health_checker_time` and
  `health_checker_timeout` are reloadable; a new key is restart-only by default
- `logging:` section: `logging.format` (`json` | `console`, default `json`),
  `logging.level` (zap levels, default `info`) and `logging.access_log`
  (default `false`). Application logs go to stderr only; the Access log (one
  JSON line per answered request, always JSON) is the only thing on stdout.
  There is no file logging. `main.go` installs a default json/info logger
  before the config parses and re-initializes it (plus the access logger)
  from the validated config after `PrepareConfig`. The access logger lives in
  `pkg/logger/access.go`; `internal/proxy` emits at request completion
  (`ReverseProxyHandler` and the `NoAliveBackends` 503 path; a Streamed
  response's line at stream end, `streamedBody.logAccessAtEnd`), gated by
  `logger.AccessLogEnabled()`

### Monitoring

`internal/monitoring`:
- Separate HTTP server for metrics (default: localhost:8001)
- Provides real-time stats: CPU, RAM, goroutines, open connections
- Prometheus metrics endpoint at `/metrics`
- Probe endpoints for orchestrators: `/healthz` (liveness) answers 200
  whenever the process runs; `/ready` answers 200 once the listeners are
  bound and 503 from the moment graceful shutdown begins
  (`Server.MarkNotReady`, called first in `performGracefulShutdown`).
  Backend health never gates either — zero Alive Backends is divisor
  working, answering 503s
- Per-backend stats: average response time, request count, last use time
- A failed gopsutil read keeps the last good CPU/memory values (logged); Backend rows never depend on it

### Main Server Flow

1. Start watching the config file → `reload.WatchConfigFile()` (before the read, so an edit during startup is queued, not missed; a watch failure is logged, not fatal)
2. Parse config file → `config.ParseConfigFile()`
3. Prepare/validate config → `config.PrepareConfig()`
4. Create the Pool + balancer → `core.NewBalancer()` (first Probe round runs synchronously here)
5. The Probe loop starts inside `NewBalancer` (`Pool.StartHealthChecker()`)
6. Start the client-facing server via `internal/server.Start()` (fasthttp serving `proxy.NewFasthttpHandler` for HTTP/1.1, net/http serving `proxy.NetHttpAdapter` for HTTP/2); it returns a stack-agnostic `Server` plus a channel that reports a Serve failure
7. Start the monitoring server via `monitoring.Start()` (binds synchronously; a bind failure is fatal; the Prometheus poller starts only after the bind)
8. Create the `reload.Reloader` (`internal/reload`), route SIGHUP to it (`reloadOnEverySignal`) and hand it to the config file watch (`ConfigFileWatch.StartReloading`)
9. Select on SIGINT/SIGTERM (graceful shutdown, `server.graceful_shutdown_timeout`, default 30s) vs a Serve failure (fatal, non-zero exit)

### Reload

`internal/reload.Reloader.Reload(trigger)` is the one entry point every
Reload trigger calls (SIGHUP and the config file watch): it re-reads the config file, runs
`config.PlanReload` against the last applied config, applies an accepted plan
via `LoadBalancer.ApplyReload`, and logs `Reload applied` (added/removed
Backend addresses) or `Reload rejected, divisor keeps serving the running
config` (with the error naming the keys). Calls are serialized; `Close()`
(graceful shutdown calls it right after `/ready` flips) waits for one in flight and refuses later
ones. `/metrics` drops the series of addresses no Backend has any more.

`reload.ConfigFileWatch` (fsnotify) watches the config file's directory, not
the file: an event naming the config path (Chmod-only ignored), or the config
path now resolving to a different file (kubelet renames the ConfigMap
`..data` symlink and never writes `config.yaml`), counts as a change. A burst
of changes Reloads once, after `ConfigFileQuietPeriod` (1s) without one
(trigger `config file change`). Edits through a symlink into another
directory, or to a single-file Docker bind mount from the host, raise no
event there: SIGHUP covers them (ADR 0006).

### Graceful Shutdown

Implemented in `performGracefulShutdown()`:
- Flips the monitoring server's `/ready` to 503 first, before anything waits,
  so orchestrators stop routing new traffic during the drain
- Closes the config file watch and the Reloader (`stopReloading`), waiting
  for a Reload in flight (it may be Probing the Backends it adds), so no
  Reload touches the Pool during shutdown
- Stops accepting new connections
- Waits for in-flight requests to complete
- Stops the monitoring server (poller first, then its listener) so nothing reads Pool stats after the next step
- Stops the Probe loop and waits for a round in flight (capped by `types.HealthCheckerStopTimeout`)
- Closes idle connections via `balancer.Shutdown()` → `Pool.Shutdown()`
- Timeout from `server.graceful_shutdown_timeout` enforced (default 30s);
  hitting it exits 1. Nothing ends an open Streamed response on shutdown, so
  one still open at the timeout does exactly that (ADR 0007)

## Code style

Code explains itself: put the meaning in names and structure. A comment earns
its place only for a constraint the code cannot show (a non-obvious library
behavior, a spec decision, a pointer to an ADR) and stays to one or two lines.

Names are explanatory, written out in full: say what the thing is or does,
using the CONTEXT.md vocabulary, so a reader needs no comment and no jump to
the definition. `StartHealthChecker` not `Start`, `runHealthCheckLoop` not
`run`, `updateLiveness(backend, isAlive)` not `apply(b, ok)`,
`AliveBackends()` not `Alive()`, `aliveBackendCount` not `n`. Idiomatic
one-letter receivers and loop indices are the only short names.

## Git

Never run `git commit` — the user makes every commit themselves. This overrides
any skill or slash command that tells you to commit (e.g.
`/mattpocock-skills:implement`). Finish the task, leave the changes in the
working tree, and end by suggesting a short commit message: a subject line, plus
at most two body lines when the change genuinely needs them.

## Key Implementation Details

### Balancer Selection
All balancers are registered in `core/balancer.go`:
```go
var balancers = map[string]func(*config.Config) pool.Balancer{
    "round-robin":   round_robin.New,
    "w-round-robin": w_round_robin.New,
    // ...
}
```
A new balancer is one package implementing `Join`/`Leave`/`Pick` plus a map entry; it never touches liveness.

### Backend Health Checking
The Pool owns it: one Probe loop per process, stopped by closing a channel and drained on `Shutdown`. A Down Backend stays registered (and in `Stats()`) and Rejoins when a Probe succeeds.

### Consistent Hashing (IP-Hash)
Uses `pkg/consistent` package implementing consistent hashing ring for stable IP-to-backend mapping.

### HTTP/2 Support
When `server.http_version: http2`, divisor serves via `net/http` (its built-in HTTP/2 server, enabled through `http.Server.Protocols`) instead of fasthttp; `internal/proxy/nethttp_adapter.go` bridges each request into a synthetic `fasthttp.RequestCtx` (via `fasthttpadaptor.ConvertNetHTTPRequestToFastHTTPRequest`) so the balancer/proxy path stays shared. A Streamed response leaves that synthetic buffered write: `forwardStreamedBody` flushes the headers, then every write (`flushingWriter`), and any error ends in `panic(http.ErrAbortHandler)` so a cut stream never ends cleanly. TLS is mandatory on this path (no h2c). Backends always speak HTTP/1.1 over plain HTTP.

## Testing Notes

- Tests use the test doubles in `mocks/mocks.go`: `MockProxy`, `MockBalancer`, `NewBackends(n)`, `RecordingBalancer` (records Join/Leave), `ProbeTable` (per-Backend Probe answers), `RequestFrom(ip)`
- `core/pool/pool_test.go` drives the Pool through `NewPool` + `ProbeAllBackends` with a `RecordingBalancer`; each balancer package has a selection-only `*_test.go` that feeds `Join`/`Leave` and asserts `Pick`; `core/balancer_test.go` covers `NewBalancer`, `Serve` (forward vs 503) and `Pick` racing a Probe round for every balancer
- Config validation tested in `pkg/config/config_test.go`; the Reload classifier in `pkg/config/reload_test.go` (YAML tables through `PlanReload`)
- `internal/reload/reload_test.go` drives the Reloader against a temp config file with a recording Target and an observed zap logger; `watch_test.go` drives the config file watch through real file edits (in place, bursts, a ConfigMap `..data` swap — skipped where symlinks need privileges, e.g. Windows) with a short quiet period and a channel Target
- Proxy behavior tested in `internal/proxy/proxy_test.go`; Streamed responses in `streaming_test.go` and `access_log_test.go`, each test run on every listener stack (`onEveryListenerStack`: fasthttp, net/http HTTP/2, net/http HTTP/1.1) against `httptest` Backends that emit delayed frames (or flood a client that stops reading), asserting what reaches the client and what the Backend sees
- `integration-test/` is a black-box Docker suite (dockertest): divisor and
  purpose-built echo backends run as containers, driven over real HTTP/1.1,
  HTTP/2, and TLS (see `docs/adr/0001-black-box-integration-suite.md` and
  `CONTEXT.md` for its vocabulary). Gated by `DIVISOR_INTEGRATION=1`; runs in
  CI via `.github/workflows/integration.yml`. Scenarios run with `t.Parallel()`,
  each on its own docker network — on a shared network a killed backend's
  freed IP could be claimed by another scenario's container while divisor's
  dialer still held the DNS-cached IP. Spec-red gating (see
  `docs/adr/0002-spec-red-gating.md` and CONTEXT.md): a test asserting agreed
  1.0 spec behavior that has not shipped yet calls `specRed(t, ...)` and skips
  unless `DIVISOR_INTEGRATION_SPEC_RED=1`, so the blocking CI job stays green
  while an advisory job runs just the spec-red set. The set is currently
  empty — the advisory job is removed from the workflow and the `specRed`
  helper is dormant until the next spec-red test lands. CI pre-builds the
  suite images with buildx layer caching and sets `DIVISOR_IT_PREBUILT=1` so
  `TestMain` skips its own `docker build`; local runs build as before.
  (Shipped and green: startup-down backends rejoining, 502 (not 500) for
  unreachable backends, staying up with 503 when all backends are down,
  non-zero exit on missing/invalid config, bounded failure with 504 for
  hanging backends via `server.proxy_timeout`, 413 for oversized bodies via
  `server.max_request_body_size`, Reload via SIGHUP and via a ConfigMap
  update adding a Backend and rejecting a restart-only key, `/ready` flipping at once even while a Reload Probes an added Backend (`BackendSpec.HealthCheckPath` makes that Probe hang) —
  `reload_test.go`; `ScenarioSpec.ConfigMapVolume` boots divisor on
  kubelet's ConfigMap volume layout and `publishConfigMapUpdate` swaps
  `..data` the way kubelet does; response streaming on HTTP/1.1 and HTTP/2 —
  `streaming_test.go`, on `ScenarioSpec.Streaming` and the Echo backend's
  `/stream` SSE endpoint: frames arrive incrementally, knob off keeps the
  504, idle streams reaped, a client disconnect closes the Backend stream,
  the Access log line at stream end.)

## Agent skills

### Issue tracker

Issues live as local markdown files under `.scratch/<feature>/` in this repo. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
