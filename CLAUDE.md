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
  configured `Backend` (config index, address, weight, Probe URL, proxy
  client, Alive flag), runs the Probe loop every `config.HealthCheckerTime`,
  moves Backends between Alive and Down, resets a Rejoining Backend's
  response-time score, builds `Stats()` rows in config order (`BackendHash`
  is the config index), and closes every proxy on `Shutdown()`. `NewPool`
  Probes once synchronously and does not start the loop;
  `StartHealthChecker()` does. `ProbeAllBackends()` is the one Probe round,
  shared by the loop and by tests, so unit tests never race a goroutine. It never imports a balancer package.
- **Balancers** (one package each: `core/round-robin`, `core/w-round-robin`,
  `core/random`, `core/least-connection`, `core/least-response-time`,
  `core/ip-hash`) — selection only. Each exports `New(cfg, backends)
  pool.Balancer`; `pool.Balancer` is `Join(*Backend)` / `Leave(*Backend)`,
  called by the Pool (single writer) on liveness transitions, and
  `Pick(ctx)`, which runs on request goroutines and returns nil when nothing
  is Alive. The slice-based ones embed `pool.Rotation` (copy-on-write Alive
  slice); ip-hash keeps one ring Node per Backend (`pkg/consistent`).

`core.NewBalancer(cfg, middlewareExecutor, proxyFunc)` is the only wiring:
it turns `cfg.Backends` into Backends, picks the balancer by `cfg.Type`,
builds the Pool with `cfg.HealthCheckerFunc`, returns nil if no Backend is
Alive after the first Probe round (or the type is unknown), otherwise starts
the loop and returns a `types.IBalancer` (`Serve`/`Stats`/`Shutdown`) — the
Pool plus the balancer as the rest of the process sees them. `Serve` answers
503 (`proxy.NoAliveBackends`) when `Pick` returns nil.

### Proxy Layer

`internal/proxy` handles the actual HTTP proxying:
- `ProxyClient` wraps `fasthttp.HostClient` for each backend
- Removes hop-by-hop headers (Connection, Keep-Alive, etc.)
- Sets `X-Forwarded-For` header with client IP
- Supports custom headers with special variables: `$remote_addr`, `$time`, `$uuid`, `$incremental`
- Tracks metrics: total requests, average response time, last use time, connection count

### Configuration System

`pkg/config`:
- YAML-based configuration with validation in `PrepareConfig()`
- Auto-sets defaults if not specified (e.g., `health_checker_time: 30s`, `type: round-robin`)
- Backend `url` is normalized to a dialable Backend address (`host:port`): optional `http://` and bare trailing slash stripped, missing port defaults to 80; path/query/userinfo and `https://` are rejected at startup (ADR 0004: backends are plaintext-only)
- HTTP/2 requires TLS (cert_file + key_file must be provided)
- Weighted round-robin with single backend auto-converts to regular round-robin

### Monitoring

`internal/monitoring`:
- Separate HTTP server for metrics (default: localhost:8001)
- Provides real-time stats: CPU, RAM, goroutines, open connections
- Prometheus metrics endpoint at `/metrics`
- Per-backend stats: average response time, request count, last use time
- A failed gopsutil read keeps the last good CPU/memory values (logged); Backend rows never depend on it

### Main Server Flow

1. Parse config file → `config.ParseConfigFile()`
2. Prepare/validate config → `config.PrepareConfig()`
3. Create the Pool + balancer → `core.NewBalancer()` (first Probe round runs synchronously here)
4. The Probe loop starts inside `NewBalancer` (`Pool.StartHealthChecker()`)
5. Start the client-facing server via `internal/server.Start()` (fasthttp for HTTP/1.1, net/http for HTTP/2); it returns a stack-agnostic `Server` plus a channel that reports a Serve failure
6. Start the monitoring server via `monitoring.Start()` (binds synchronously; a bind failure is fatal; the Prometheus poller starts only after the bind)
7. Select on SIGINT/SIGTERM (graceful shutdown, 30s timeout) vs a Serve failure (fatal, non-zero exit)

### Graceful Shutdown

Implemented in `performGracefulShutdown()`:
- Stops accepting new connections
- Waits for in-flight requests to complete
- Stops the monitoring server (poller first, then its listener) so nothing reads Pool stats after the next step
- Stops the Probe loop and waits for a round in flight (capped by `types.HealthCheckerStopTimeout`)
- Closes idle connections via `balancer.Shutdown()` → `Pool.Shutdown()`
- 30-second timeout enforced

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
var balancers = map[string]func(*config.Config, []*pool.Backend) pool.Balancer{
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
When `server.http_version: http2`, divisor serves via `net/http` + `golang.org/x/net/http2` instead of fasthttp; `internal/proxy/nethttp_adapter.go` bridges each request into a synthetic `fasthttp.RequestCtx` so the balancer/proxy path stays shared. TLS is mandatory on this path (no h2c). Backends always speak HTTP/1.1 over plain HTTP.

## Testing Notes

- Tests use the test doubles in `mocks/mocks.go`: `MockProxy`, `MockBalancer`, `NewBackends(n)`, `RecordingBalancer` (records Join/Leave), `ProbeTable` (per-Backend Probe answers), `RequestFrom(ip)`
- `core/pool/pool_test.go` drives the Pool through `NewPool` + `ProbeAllBackends` with a `RecordingBalancer`; each balancer package has a selection-only `*_test.go` that feeds `Join`/`Leave` and asserts `Pick`; `core/balancer_test.go` covers `NewBalancer`, `Serve` (forward vs 503) and `Pick` racing a Probe round for every balancer
- Config validation tested in `pkg/config/config_test.go`
- Proxy behavior tested in `internal/proxy/proxy_test.go`
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
  `server.max_request_body_size`.)

## Agent skills

### Issue tracker

Issues live as local markdown files under `.scratch/<feature>/` in this repo. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
