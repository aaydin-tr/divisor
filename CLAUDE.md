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

### Release benchmark
```bash
# Full local Release benchmark of this checkout (Docker; ~4 min per Contender;
# never publishes). Prints the Markdown section, results in benchmark/results/
make benchmark

# Smoke run (1 round, no warm-up, 3s per Workload): exits non-zero when a
# Contender is not in the Equivalent setup. The "Benchmark smoke" PR check runs it
make benchmark-smoke

# The benchmark module's own tests (its own Go module, like integration-test;
# CI runs them with -race)
go -C benchmark test -race ./...

# Re-render section.md and benchmark.json from a saved results directory
go -C benchmark run ./cmd/release-benchmark report --results results
```
`benchmark/` (see its README): `plan` (pure: Contenders, Workloads, schedule,
resources), `run` (Docker + oha, records `recording.json` and `raw/`;
`peakmemory.go` reads the Contender's `memory.peak` after Throughput, n/a if unreadable;
`warmPageCache` starts each Contender once, unmeasured, before round 1 so its files are cached),
`report` (pure: medians, spread, failed rows, Markdown in `markdown.go`,
`benchmark.json` in `benchmarkjson.go`; one golden built from Go data,
`report/testdata/expected-section.md` and `expected-benchmark.json`, which `-update` rewrites,
plus table tests for the special cases; the "Benchmark smoke" PR check covers real runs), `oha` (oha's flags and JSON fields),
`backend` (the Benchmark backend), `contenders/<name>/` (Dockerfile + config
per Contender: divisor, caddy, nginx, traefik; an upstream Dockerfile's `FROM`
tag is its only version pin and, embedded by `contenders.PinnedUpstreamImage`,
the Version column), `loadgenerator/Dockerfile` (the oha pin), `equivalentsetup`
(smoke mode's checks: `Check`/`Verify` pure, `Collect` sends the requests from
the `cmd/equivalent-setup-sampler` container on the host network),
`releasenotes` (Splice: the section between the `release-benchmark:begin`/`end`
markers of a release's notes; `release-benchmark splice` runs it).

### Go toolchain bump
A bump moves the `go` line of every module (`go.mod`, `integration-test/`,
`integration-test/echobackend/`, `benchmark/`) and `.golangci.yml`, the
workflows' `go-version`, and the `golang:` base images
(`integration-test/divisor.Dockerfile`, `integration-test/echobackend/Dockerfile`,
`benchmark/backend/Dockerfile`, `benchmark/equivalentsetup/Dockerfile`). Dependabot's
monthly docker entry (`.github/dependabot.yml`) bumps only the upstream Contender and oha
pins, in one grouped PR: never these bases, nor `contenders/divisor` (the image under test).

### Install
```bash
go install github.com/aaydin-tr/divisor@latest

# Docker image (config read from /etc/divisor/config.yaml)
docker run -p 8080:8080 -v "$PWD/config.yaml:/etc/divisor/config.yaml:ro" ghcr.io/aaydin-tr/divisor:latest

# Helm chart (divisor config under `config:` in values.yaml)
helm install divisor oci://ghcr.io/aaydin-tr/charts/divisor -f values.yaml
```

## Architecture

### Core Load Balancing System

`core/` has two halves (vocabulary in `CONTEXT.md`):

- **Pool** (`core/pool`: `pool.go`, `backend.go`, `rotation.go`) — owns every
  configured `Backend` (Index, address, weight, Probe URL, proxy
  client, Alive flag), runs the Probe loop every `config.HealthCheckerTime`,
  moves Backends between Alive and Down, calls `ProbeRoundStarted()` before
  each round's Probes on a `pool.ProbeRoundBalancer`, builds `Stats()` rows
  in config order (`BackendHash` is the Backend's Index: its config position
  at boot, a never-used-before number for a Backend a Reload added), and closes every proxy on
  `Shutdown()`. `NewPool` Probes once synchronously and does not start the
  loop; `StartHealthChecker()` does. `ProbeAllBackends()` is the one Probe
  round, shared by the loop and by tests, so unit tests never race a
  goroutine. A Reload goes through `SetProbeSettings()` (only when it changes them: it restarts the loop timer) and
  `ReplaceBackends()` (added Backends Probed concurrently and Joined, removed ones Leave
  and close, survivors untouched except that a `pool.RingBalancer` re-places one whose
  `AddressOccurrence` shifted); `transitionsMu` makes Probe rounds and
  Reloads the Balancer's one writer. It never imports a balancer package.
- **Balancers** (one package each: `core/round-robin`, `core/w-round-robin`,
  `core/random`, `core/least-connection`, `core/least-response-time`,
  `core/ip-hash`) — selection only. Each exports `New(cfg)
  pool.Balancer`; `pool.Balancer` is `Join(*Backend)` / `Leave(*Backend)`,
  called by the Pool (single writer) on liveness transitions and Reloads, and
  `Pick(ctx)`, which runs on request goroutines and returns nil when nothing
  is Alive. The slice-based ones embed `pool.Rotation` (copy-on-write Alive
  slice); ip-hash (a `pool.RingBalancer`) places each Backend on the ring
  (`pkg/consistent`, generic over the position's owner) by its address and
  `AddressOccurrence`, never its Index, so divisor instances sharing a config
  agree whatever their Reload history (ADR 0008); a Backend Joining under a
  ring key takes over the positions of the entry it replaces.
  least-response-time (a `pool.ProbeRoundBalancer`) owns its Score
  lifecycle: `Join` resets a Backend to unmeasured, and `ProbeRoundStarted`
  expires the Score of one traffic bypassed for 3 rounds (requests went to
  other Backends, none to it; a round with no requests at all counts for
  nobody), so `Pick` sends it one Trial request (`ClaimTrialRequest`).
  Otherwise `Pick` is power of two choices without replacement over Score ×
  (Pending requests + 1), an unmeasured Backend scored at the median.

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
- Response batching (`server.disable_response_batching`, on by default; ADR 0010 holds the measurements): `NewFasthttpHandler` calls
  `runtime.Gosched()` before fasthttp writes a response that isn't streamed, so one netpoll batch's responses
  go out back-to-back (measured +41-48% req/s on one core, +19-28% at GOMAXPROCS 2 with a 2-core cpuset or a
  1- or 2-CPU quota). The config owns the CPU rule: `prepareServer` sets `DisableResponseBatching` to true
  whenever GOMAXPROCS at startup (`startupGOMAXPROCS`, read once so a Reload compares like with like) is above
  2, where the yield only delays the write (-7% at 4 cores); the handler just takes the flag. The tests'
  "fasthttp on one core" listener stack covers it. Go rounds a CPU quota under 2 up to GOMAXPROCS 2, so a
  `limits.cpu: 1` pod batches at GOMAXPROCS 2
- Keeps least-response-time's Score (`RecentResponseTime`, a lock-free moving average) and its
  Trial request state (`noTrialRequest` → `trialRequestDue` on `ExpireRecentResponseTime`, which
  leaves a due or claimed one alone → `trialRequestClaimed` on `ClaimTrialRequest`): the next sample replaces the Score instead of
  averaging in (`consumeScoreExpiry`), and an `OnRequest` Middleware answering the claimed
  request hands it back (`handBackTrialRequest`)
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
ones. `/metrics` labels each Backend's series by `address` and `index` (its Index) and drops a removed Backend's series.

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

## Release / distribution

A `v*` tag runs `.github/workflows/goreleaser.yml`:
- goreleaser (`.goreleaser.yaml`) builds the binaries (ldflags set `main.version`/`main.commit`)
  and, via `dockers_v2`, a `linux/amd64`+`linux/arm64` image pushed to `aaydintr/divisor` and
  `ghcr.io/aaydin-tr/divisor`, tagged `vX.Y.Z`, plus `vX.Y` and `latest` unless a prerelease;
  a prerelease tag (`vX.Y.Z-rc.N`) also becomes a GitHub prerelease, never the "Latest" release
- The root `Dockerfile` is that release image: it copies goreleaser's prebuilt binary onto
  distroless `static-debian13:nonroot` (no shell). The integration suite builds its own Alpine
  image from `integration-test/divisor.Dockerfile`; never point the suite at the root one
- `charts/divisor` is packaged by `.github/scripts/helm-package-chart.sh` (version = tag
  without `v`, appVersion = tag, so it defaults to that release's image) and pushed to
  `oci://ghcr.io/aaydin-tr/charts`
- The chart and `examples/kubernetes` ship the same divisor config and manifests; change both.
  `TestHelmChartConfigMatchesTheKubernetesExample` (`pkg/config/config_strict_test.go`)
  guards the config
- `.github/workflows/benchmark.yml` runs the Release benchmark (ADR 0009), its own workflow so
  a benchmark failure never touches the release. Triggers: `workflow_run` of `goreleaser`
  completing successfully (ref = that run's `head_branch`, the tag; `--trigger release`: Plan
  skips a prerelease tag and the summary says so) and `workflow_dispatch` with a `ref` input
  (any tag or branch; `--trigger manual`, an rc runs). The tool and Contender definitions come
  from the workflow's own commit; the measured ref is a second checkout in `measured/` (full
  history plus `git fetch --force --tags`, for tag messages and the release tag check). A tag runs its
  published `ghcr.io/aaydin-tr/divisor:<tag>`; a branch is built into the release image shape,
  so no `setup-buildx-action` (the default `docker` driver must see that local image).
  The run job is read-only, one concurrency group per ref, 60-minute timeout. The tool appends the
  section, or the skip/failure reason, to `$GITHUB_STEP_SUMMARY` (`--summary`), closed by the
  publish verdict line; a failed row exits non-zero after everything is written;
  `benchmark/results` is always uploaded as the `release-benchmark-results` artifact
- Publishing (ADR 0009): Plan requests it when a release run's annotated tag message has the line
  `benchmark: publish` (`make tag ... BENCHMARK=publish`), or a manual run on a release tag with a
  GitHub release ticks the `publish` input (ticked on a branch: an error before anything runs).
  Report's verdict is requested and no failed row; the tool writes `publish=true|false` to
  `$GITHUB_OUTPUT` (`--publish-verdict`). Only then does the `publish` job run, the only job with
  `contents: write`: it splices `section.md` into the notes (`release-benchmark splice`, between
  the markers `.goreleaser.yaml`'s footer writes, else above GoReleaser's footer) with `gh release
  edit` and uploads `benchmark.json` with `--clobber`, so publishing again replaces both

PRs touching release files (`.goreleaser.yaml`, `Dockerfile`, `main.go`, `charts/`,
`.github/scripts/`, the release workflows) run the "Release dry-run" job
(`.github/workflows/release-dry-run.yml`): a goreleaser snapshot, the snapshot image's
`--version` against `dist/metadata.json`, and `.github/scripts/helm-chart-smoke-test.sh`
(helm lint, kubeconform `-strict` on the default render and on one with `resources` set, the default
image tag).

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
The Pool owns it: one Probe loop per process, stopped by closing a channel and drained on `Shutdown`. A Down Backend stays registered (and in `Stats()`) and Rejoins when a Probe succeeds, its pooled connections closed first (it may have restarted while Down).

### Consistent Hashing (IP-Hash)
Uses `pkg/consistent` package implementing consistent hashing ring for stable IP-to-backend mapping.

### HTTP/2 Support
When `server.http_version: http2`, divisor serves via `net/http` (its built-in HTTP/2 server, enabled through `http.Server.Protocols`) instead of fasthttp; `internal/proxy/nethttp_adapter.go` bridges each request into a synthetic `fasthttp.RequestCtx` (via `fasthttpadaptor.ConvertNetHTTPRequestToFastHTTPRequest`) so the balancer/proxy path stays shared. A Streamed response leaves that synthetic buffered write: `forwardStreamedBody` flushes the headers, then every write (`flushingWriter`), and any error ends in `panic(http.ErrAbortHandler)` so a cut stream never ends cleanly. TLS is mandatory on this path (no h2c). Backends always speak HTTP/1.1 over plain HTTP.

## Testing Notes

- Tests use the test doubles in `mocks/mocks.go`: `MockProxy` (counts the requests it served, as `TotalRequestCount`/`Stat` report them), `MockBalancer`, `NewBackends(n)`, `RecordingBalancer` (records Join/Leave), `ProbeTable` (per-Backend Probe answers), `RequestFrom(ip)`
- `core/pool/pool_test.go` drives the Pool through `NewPool` + `ProbeAllBackends` with a `RecordingBalancer` (the real ip-hash Balancer where ring placement after a Reload is the point); each balancer package has a selection-only `*_test.go` that feeds `Join`/`Leave` and asserts `Pick`; `core/balancer_test.go` covers `NewBalancer`, `Serve` (forward vs 503) and `Pick` racing a Probe round for every balancer
- Config validation tested in `pkg/config/config_test.go`; the Reload classifier in `pkg/config/reload_test.go` (YAML tables through `PlanReload`)
- `internal/reload/reload_test.go` drives the Reloader against a temp config file with a recording Target and an observed zap logger; `watch_test.go` drives the config file watch through real file edits (in place, bursts, a ConfigMap `..data` swap — skipped where symlinks need privileges, e.g. Windows) with a short quiet period and a channel Target
- Proxy behavior tested in `internal/proxy/proxy_test.go`; Streamed responses in `streaming_test.go` and `access_log_test.go`, each test run on every listener stack (`onEveryListenerStack`: fasthttp, net/http HTTP/2, net/http HTTP/1.1) against `httptest` Backends that emit delayed frames (or flood a client that stops reading), asserting what reaches the client and what the Backend sees
- The benchmark module (`benchmark/`, its own Go module) runs its unit tests with `-race` and its own golangci-lint step in the Test and Linter workflows. The "Benchmark smoke" PR check (`.github/workflows/benchmark-smoke.yml`, wired into `pr.yml` like the Release dry-run: a detect job runs it only when `benchmark/`, `.github/workflows/benchmark*.yml`, or the release image inputs it rebuilds — `Dockerfile`, `main.go`, `.goreleaser.yaml` — changed) builds the PR's divisor and runs `release-benchmark --smoke`: every Contender must be ready, answer only 2xx, spread evenly over the 4 Benchmark backends, forward `X-Forwarded-For` and keep its Backend connections alive (`benchmark/equivalentsetup`), else the job fails naming the Contender and the check
- `integration-test/` is a black-box Docker suite (dockertest): divisor and
  purpose-built echo backends run as containers (divisor from the suite's own
  Alpine `integration-test/divisor.Dockerfile`, since scenarios need
  `/bin/sh`; the root `Dockerfile` is the distroless release image goreleaser
  builds), driven over real HTTP/1.1,
  HTTP/2, and TLS (see `development/adr/0001-black-box-integration-suite.md` and
  `CONTEXT.md` for its vocabulary). Gated by `DIVISOR_INTEGRATION=1`; runs in
  CI via `.github/workflows/integration.yml`. Scenarios run with `t.Parallel()`,
  each on its own docker network — on a shared network a killed backend's
  freed IP could be claimed by another scenario's container while divisor's
  dialer still held the DNS-cached IP. Spec-red gating (see
  `development/adr/0002-spec-red-gating.md` and CONTEXT.md): a test asserting agreed
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
  the Access log line at stream end; response batching — `responsebatching*_test.go`
  runs each scenario on a one-CPU divisor (`ScenarioSpec.CPULimit: 1` adds `GOMAXPROCS=1`:
  Go rounds a quota under 2 up to 2) with batching on and off, under the loads in `load_test.go`;
  keep-alive and pipelining also run on two CPUs, and keep-alive on three, where it never batches.)

## Agent skills

### Issue tracker

Issues live as local markdown files under `.scratch/<feature>/` in this repo. See `development/agents/issue-tracker.md`.

### Domain docs

Single-context: one `CONTEXT.md` at the repo root, ADRs in `development/adr/`. See `development/agents/domain.md`.

### Docs by audience

`docs/` (plus `README.md`, `examples/`) is for people who use divisor: it never links ADRs,
`CONTEXT.md`, test names or CI internals, and explains a term where it first uses it.
Development material lives in `development/` and `CONTRIBUTING.md`.
