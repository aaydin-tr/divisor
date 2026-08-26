# TODO — 1.0 backlog

Working list for the 1.0 release (breaking changes allowed). The 1.0 scope was
grilled and settled 2026-08-25 (grilling + domain-modeling); each unshipped
item carries its agreed design, so implementation starts from a decision, not
a discussion. Items marked **[born-red]** have an integration test in
`integration-test/` that already asserts the target behavior and stays red
until it ships. ADRs are written at implementation time (the repo's pattern);
items that need one say so. Each open item below points to its spec
(`.scratch/<feature>/spec.md`) and its ready-for-agent tickets
(`.scratch/<feature>/issues/`); conventions in `docs/agents/issue-tracker.md`.

## Observability

Spec: `.scratch/logging/spec.md` — tickets in `.scratch/logging/issues/`.

- [x] Logging rework — shipped: file logging deleted entirely
  (`pkg/logger/logfile.go`, `GetLogFile`, the platform log-dir logic gone;
  the Dockerfile no longer creates `/var/log/divisor`). Application logs go
  to **stderr** only, JSON by default; stdout is reserved for the Access
  log. New `logging:` config section: `logging.format: json | console`
  (default **json**) and `logging.level` (default `info`), validated in
  `PrepareConfig` with errors naming the invalid value; failures before the
  config parses use a default json/info logger. Breaking change: no file
  sink, default encoding console → JSON. Ticket: `01-app-logs-json-stderr.md`
  (done). Integration: `TestApplicationLogsAreJSONOnStderrByDefault`,
  `TestApplicationLogsConsoleFormat`.
- [x] Access log (see CONTEXT.md **Access log**) — shipped: one JSON line per
  request divisor answers on stdout (always JSON, whatever `logging.format`),
  off by default behind `logging.access_log: true`. Fixed field set: `time`
  (RFC 3339 ms UTC), `client_ip`, `method`, `path`, `status`, `backend` and
  `request_seq` (both omitted on the zero-Alive 503 path), `duration_ms`,
  `bytes_out`, `short_circuit: true` when a Middleware answered. Emitted at
  request completion in `internal/proxy` (`ReverseProxyHandler` +
  `NoAliveBackends`), gated by `logger.AccessLogEnabled()`. Hot-path cost
  A/B-measured like H5 (results in the spec's Comments): 3.3 ns/request when
  off, ~1 µs emit when on. Ticket: `02-access-log.md` (done). Integration:
  `TestAccessLogOneJSONLinePerRequestOnStdout`.

## Distribution

Spec: `.scratch/distribution/spec.md` — tickets in
`.scratch/distribution/issues/`.

- [ ] Official Docker release — goreleaser publishes the same image to Docker
  Hub (`aaydin-tr/divisor`, where images get discovered) and GHCR
  (`ghcr.io/aaydin-tr/divisor`, the no-friction k8s pull), multi-arch
  `linux/amd64` + `linux/arm64` via `docker_manifests`, tags `latest` +
  `vX.Y.Z` + `vX.Y`. Base image `gcr.io/distroless/static` running as
  **nonroot** — no shell on a network-facing tool, and it ships the Debian
  CA bundle, so a future https-to-backend feature needs no base change.
  Depends on the logging rework (no writable `/var/log/divisor` required)
  and the bind-default flip under Config surface (today's image is
  unreachable with a default-`localhost` config). Ticket:
  `02-docker-image.md`.
- [ ] Helm chart — OCI chart pushed to GHCR
  (`helm install oci://ghcr.io/aaydin-tr/charts/divisor`), released by the
  existing tag-triggered goreleaser CI (reuses the GHCR login the Docker
  release needs), versioned with the app tag. Ticket: `03-helm-chart.md`.
- [x] `--version` flag + version in the startup log — shipped: `divisor
  --version` prints `divisor version X.Y.Z, build <short-commit>` and exits 0
  (`dev` placeholders on non-release builds); the startup log's first line
  names the same build, before anything can fail. Goreleaser injects the
  values via ldflags (`main.version`/`main.commit`). Build date dropped by
  decision 2026-08-27 (no peer CLI prints one). Ticket: `01-version-flag.md`
  (done). Integration: `TestVersionFlagPrintsAndExitsZero`.

## Kubernetes

Target: divisor runs first-class in k8s — a Deployment fronting pods (via a
headless Service or pod IPs), the L7 algorithms being the value-add over a
plain Service. The zero-alive-at-boot fix under "1.0 behavior fixes" is part
of this story: the LB must boot before its Backends.

Spec: `.scratch/kubernetes/spec.md` — tickets in `.scratch/kubernetes/issues/`
(the zero-alive-at-boot fix is ticket `01-zero-alive-at-boot.md` there).

- [x] Backend DNS re-resolution — shipped: `server.dns_cache_duration`
  (default 60s, the library default; negative rejected naming the key)
  reaches both the proxy dialer (copied onto every Backend by
  `PrepareConfig`, like `proxy_timeout`) and the Probe client (which
  previously pinned resolved IPs for a full hour), so routing and liveness
  agree on addresses. The k8s example manifests should set it to a few
  seconds. Ticket: `03-dns-cache-duration.md` (done).
- [x] Readiness/liveness endpoints on the monitoring server — shipped:
  `/healthz` (liveness) answers 200 whenever the process runs; `/ready`
  answers 200 once the listeners are bound and 503 from the moment graceful
  shutdown begins (`monitoring.Server.MarkNotReady`, called first in
  `performGracefulShutdown`, before the drain). **Backend health never gates
  either** — zero Alive Backends is divisor *working* (answering 503s, per
  zero-alive-at-boot). Unblocks the monitoring-coverage item under
  Suite / CI. Ticket: `02-probe-endpoints.md` (done). Integration:
  `TestProbeEndpointsDuringServeAndShutdown`,
  `TestAllBackendsDownAtBootServes503ThenRejoins` (probes with zero Alive
  Backends).
- [ ] Example manifests — Deployment + ConfigMap + Service in the repo, with
  probes wired to `/healthz`/`/ready` and `monitoring.host: 0.0.0.0`
  (kubelet probes hit the pod IP; the localhost default cannot serve them —
  the default itself stays localhost for non-k8s safety). Ticket:
  `06-example-manifests.md`.
- [ ] Reload (see CONTEXT.md **Reload**) — apply an edited config file
  without restart. Scope: **the Backend set and Probe settings only** (the
  Pool already knows Join/Leave); an edit touching any other key is rejected
  — divisor keeps serving with the old config and logs the offending key.
  An invalid new config is handled the same way. Triggers: file watch
  (fsnotify, debounced, following the ConfigMap symlink-swap pattern) plus
  SIGHUP as the manual/scripted trigger. Record as an ADR when implemented.
  Do it born-red: integration scenario edits the mounted config and asserts
  the added Backend receives traffic without a container restart.
  Tickets: `04-reload-via-sighup.md`, `05-reload-file-watch.md`.

## Streaming

Spec: `.scratch/streaming/spec.md` — tickets in `.scratch/streaming/issues/`
(`04-streamed-access-log.md` there covers the Access-log interaction, which
is specced but has no bullet below).

Reopens review.md L8 (declared unsupported 2026-08-23; superseded). Response
streaming ships in 1.0. Record the model as an ADR when implemented; it
amends ADR 0003 (bounded proxy timeout) twice — the timeout model and the L6
cancellation note. Design, settled 2026-08-25:

- [ ] Opt-in knob `server.streaming: true`, default off — the release cannot
  regress anyone who doesn't opt in; candidate for default-on in 1.1, when
  the knob can be deleted. When on, a response streams (see CONTEXT.md
  **Streamed response**) when the Backend response has
  `Content-Type: text/event-stream` **or** no `Content-Length` (chunked);
  everything else buffers exactly as today, including `OnResponse` body
  rewrites. Both stacks: fasthttp via `StreamResponseBody`; the net/http
  adapter grows a real flusher path. Tickets: `01-fasthttp-streaming.md`,
  `02-http2-streaming.md`.
- [ ] Timeout model — for a Streamed response, `proxy_timeout` bounds
  time-to-first-response-byte; a new `server.stream_idle_timeout` (default
  5m, **no infinite setting**) kills a stream with no data flowing. Every
  connection keeps *a* bound — ADR 0003's spirit, amend its Consequences.
  Tickets: behavior in `01-fasthttp-streaming.md`/`02-http2-streaming.md`;
  the ADR amendment in `05-streaming-adr-and-contract-docs.md`.
- [ ] Client cancellation for streamed responses, both stacks — a client
  disconnect aborts the Backend stream (fasthttp exposes the connection on
  the streaming path; net/http has `r.Context()`). Without it, every
  disconnected SSE client leaks a Backend connection for as long as the
  Backend keeps sending — this is a requirement of shipping streaming, not
  an option. Buffered requests keep the settled L6 position (no
  cancellation, bounded by `proxy_timeout`); scope ADR 0003's L6 note to
  buffered requests when amending. Ticket: `03-client-disconnect-aborts.md`.
- [ ] Middleware contract — `OnResponse` runs on a Streamed response's
  status and headers before the first body byte is forwarded; Short-circuit
  still works there (replace the stream with a crafted response); the body
  is never seen or rewritten. Document in the contract doc comment, README,
  and CONTEXT.md. Tickets: behavior in the `01`/`02` streaming tickets;
  documentation in `05-streaming-adr-and-contract-docs.md`.
- [ ] Request streaming: **deliberately post-1.0** — uploads stay buffered
  and capped by `max_request_body_size` (rejecting an oversized body up
  front needs the buffer); streamed uploads reopen that 413-enforcement
  design and are additive later.

## Config surface additions

Spec: `.scratch/config-surface/spec.md` — tickets in
`.scratch/config-surface/issues/`.

New `server`/backend options to expose in `pkg/config` (today these are either
hardcoded or silently left at library defaults):

- [x] `server.max_request_body_size` — shipped: exposed in `pkg/config`
  (default 4MB, zero/unset means the default), applied on both paths (fasthttp
  `MaxRequestBodySize` + `proxy.ErrorHandler` for the 413; `nethttp_adapter.go`
  rejects declared Content-Length over the cap up front and buffers
  length-less bodies up to the cap, mirroring fasthttp's own chunked-body
  handling, so an oversized payload never reaches a backend), returns
  **413 Request Entity Too Large** when exceeded. **[spec-red:**
  `TestProxyMatrix/BodyOverLimit413`, `TestHTTP2/BodyOverLimit413` **— now
  green]**
- [x] `server.proxy_timeout` — shipped: global knob, default 60s, no
  "unlimited" setting (see `docs/adr/0003-bounded-proxy-timeout.md`);
  `internal/proxy` now calls `DoTimeout` and surfaces expiry as **504** (502
  stays reserved for refused/reset connections), no retry on another Backend.
  A per-backend `backends[].proxy_timeout` override is deferred (additive,
  non-breaking). **[spec-red:** `TestPausedBackendBoundedFailure` **— now
  green]**
- [x] Default bind addresses — shipped: the client server's `host` default is
  now `0.0.0.0` (a load balancer accepts outside traffic; the old localhost
  default made the Docker image silently unreachable). Monitoring keeps its
  `localhost` default (exposure stays opt-in). Breaking change, flagged in
  the migration-notes ticket. Ticket: `01-bind-default-flip.md` (done).
  Integration: `TestDefaultBindIsReachableFromOutsideContainer` (the
  harness's `OmitHost` knob runs divisor on its own default).
- [x] `server.read_buffer_size` / `server.write_buffer_size` — shipped:
  HTTP/1.1-path knobs on the fasthttp server (unset or non-positive means
  the 4096 default, per the config module's `<= 0 → default` convention).
  Ticket: `02-header-buffer-sizes.md` (done). Integration:
  `TestOversizedHeadersNeedRaisedReadBufferSize` (431 at the 4KB default,
  200 once raised).
- [x] `server.max_conns_per_ip`, `server.max_requests_per_conn` — shipped:
  unset or non-positive means unlimited (library default, same `<= 0 →
  default` convention); wired to the fasthttp server, seam-tested. Ticket:
  `03-connection-caps.md` (done).
- [x] `server.graceful_shutdown_timeout` — shipped: replaces the hardcoded
  30s in `performGracefulShutdown` (default stays 30s; zero means the
  default, negative rejected naming the key). Ticket:
  `04-graceful-shutdown-timeout.md` (done).
- [ ] HTTP/2 server tuning (existing `TODO` at `main.go:158`) — pass a
  configured `http2.Server` (e.g. `max_concurrent_streams`, `idle_timeout`)
  instead of the zero value. Ticket: `05-http2-server-tuning.md`.
- [ ] Health Probe tuning — probe timeout and expected-status are hardcoded
  (GET, only 200 counts as Alive, client defaults in `pkg/http`); consider
  `health_checker_timeout` and per-backend expected status. Ticket:
  `06-probe-tuning.md`.
- [x] TLS tuning — shipped: `server.tls_min_version` accepts `"1.2"` and
  `"1.3"` (anything else fails startup naming the key; unset keeps the
  runtime default), mapped onto the TLS config of both stacks. Cipher suites
  deliberately not exposed in 1.0. Ticket: `07-tls-min-version.md` (done).
  Integration: `TestTLSMinVersion13RefusesTLS12`.

(Backend DNS re-resolution moved to the Kubernetes section.)

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
- [x] Zero Alive Backends at boot → start anyway, serve 503, let Backends
  Rejoin — fixed: `core.NewBalancer` dropped its
  `pool.AliveBackendCount() == 0 → nil` check (it now logs a warning and
  serves 503 until a Probe lets a Backend Rejoin); `main.go`'s nil check
  stays as a defensive backstop for the unknown-type case (exits non-zero).
  Unit test flipped: `TestNewBalancerServes503UntilAProbeSucceedsWhenNothingIsAliveAtStartup`
  covers all six algorithms (ip-hash ring included). Trade-off accepted: a
  typo'd backend URL no longer fails fast at boot — liveness stays visible
  in `/stats` and logs, which is why the readiness endpoints shipped
  alongside. Landed together with its integration test, so it never needed
  spec-red gating. Ticket: `.scratch/kubernetes/issues/01-zero-alive-at-boot.md`
  (done). Integration: `TestAllBackendsDownAtBootServes503ThenRejoins`.
- [x] X-Forwarded-For semantics — settled 2026-08-25: **append stays** (as
  shipped by L1), and 1.0 adds no trusted-proxy config (post-1.0, additive).
  `TestProxyMatrix/XForwardedFor` stays pinned to append.
- [x] Dockerfile/entrypoint: divisor exits **0** when the config file is
  missing or invalid — fixed: every startup-failure path in `main.go` (empty
  flag, missing file, parse error, `PrepareConfig` error, middleware error,
  nil balancer, listen/server-start error) now uses `zap.S().Fatal*`, which
  exits 1 after syncing the log. **[born-red:**
  `TestConfigErrorExitsNonZero` **— now green]**

## Docs

Spec: `.scratch/docs/spec.md` — tickets in `.scratch/docs/issues/`.

- [ ] Restructure — README slims to overview + quickstart; `docs/` gains:
  config reference (every key, its default, since-version), middleware
  guide, deployment guides (Docker, k8s + Helm, systemd), observability
  guide (access logs, Prometheus, a Loki pipeline example). Plain Markdown,
  browsable on GitHub — no site tooling in 1.0; a published site
  (mkdocs-material on GitHub Pages) is the immediate post-1.0 follow-up,
  and the content transfers unchanged. Tickets:
  `01-docs-tree-and-readme-slim.md` through `06-migration-notes.md`
  (docs tree + README, middleware guide, config reference, deployment
  guides, observability guide, migration notes).

## Suite / CI follow-ups

- [x] Docker layer caching for the Integration job — CI now builds both suite
  images with buildx + `type=gha` cache (`.github/workflows/integration.yml`)
  and sets `DIVISOR_IT_PREBUILT=1` so `TestMain` skips its own `docker build`
  (it verifies the images exist instead). Pushes to `main` also run the suite
  (`go.yml`) to seed the base-branch cache, since PR-scoped caches are not
  shared across PRs. Local runs are unchanged.
- [x] `t.Parallel` across scenarios — every top-level test calls
  `t.Parallel()`; scenarios were already isolated by container/network names.
- [x] Spec-red gating decided (see `docs/adr/0002-spec-red-gating.md`):
  spec-red tests call `specRed(t, ...)` and skip unless
  `DIVISOR_INTEGRATION_SPEC_RED=1`; the blocking Integration job stays green
  while an advisory `continue-on-error` job runs just the spec-red set.
  Shipping a spec item = remove its `specRed` call and drop it from the
  advisory job's `-run` pattern. The set is currently **empty** (both original
  spec-red tests shipped), so the advisory job is removed from
  `.github/workflows/integration.yml`; the `specRed` helper stays dormant —
  re-add the job when the next spec-red test lands
  (zero-alive-Backends-at-boot was planned as one, but landed together with
  its fix, born green).
- [ ] Dependency upgrade pass — `go get -u ./...` + `go mod tidy` in both
  modules (the root module and `integration-test/`, which pins its own
  dependency set — keep shared pins like fasthttp in sync between them),
  then the full suite (`go test -race ./...` plus the integration suite).
  Natural companion to the toolchain bump below — land them in one pass or
  back to back.
- [ ] Go toolchain update to 1.27 (latest) — currently pinned to 1.25.3 in
  `go.mod`, `integration-test/go.mod`, all five workflow files under
  `.github/workflows/` (`go-version:`), and the Dockerfile builder stage
  (`golang:1.25-alpine`). Bump everywhere in one pass and run the full suite
  (`go test -race ./...` plus the integration suite).
- [ ] Monitoring server coverage (explicitly out of scope for the first
  suite) — **now unblocked**: the readiness/liveness endpoints shipped
  (ticket `.scratch/kubernetes/issues/02-probe-endpoints.md`), de-conflating
  divisor liveness from Backend health, so `/metrics` can be tested with
  zero Alive Backends. The harness's `ExposeMonitoring` scenario knob
  already publishes the monitoring port.

## Suggested order

1. **Logging rework** — small, breaking, and it unblocks the Access log and
   the Docker image.
2. **Zero-alive-at-boot + readiness endpoints** — they pair (readiness is
   what makes boot-with-dead-Backends operable), and both are prerequisites
   for the k8s story.
3. **Reload** — the last piece of engineering the k8s section needs.
4. **Streaming** — the long pole; start it early enough that it doesn't gate
   the tag.
5. **Distribution** (Docker, Helm, `--version`) — near-free, land any time
   before the release tag.
6. **Config-surface knobs and Docs** — batched as convenient; docs finish
   last, when the surface has stopped moving.

## Working order (effort-optimized, respects the suggested order — 2026-08-26)

Item-level ordering of everything still open: easiest first within each tier,
tiers sequenced so no item lands before its prerequisites, and the big rocks
keep their suggested-order positions (reload → streaming → distribution
flexible → docs last).

### Tier 1 — trivial config knobs (each an afternoon or less) — **all seven shipped 2026-08-26**

1. Default bind flip (`01-bind-default-flip.md`) — one default change;
   **unblocks the Docker image**, so it goes first.
2. `--version` flag (`01-version-flag.md`) — goreleaser ldflags; distribution's
   easiest piece, land it now.
3. `server.graceful_shutdown_timeout` (`04-graceful-shutdown-timeout.md`) —
   lift the hardcoded 30s into config.
4. `server.read_buffer_size` / `server.write_buffer_size`
   (`02-header-buffer-sizes.md`) — pass-through knobs.
5. `server.max_conns_per_ip` / `server.max_requests_per_conn`
   (`03-connection-caps.md`) — same shape.
6. TLS min version (`07-tls-min-version.md`) — one key onto `crypto/tls`.
7. Backend DNS re-resolution (`03-dns-cache-duration.md`) — expose fasthttp's
   `DNSCacheDuration`; **unblocks the example manifests** (they set it short).

### Tier 2 — mechanical but wide

8. Go toolchain bump to 1.27 + dependency upgrade pass — one combined pass
   (both go.mod files, five workflows, Dockerfile), full suite after; doing it
   here means everything below ships on the new toolchain.
9. HTTP/2 server tuning (`05-http2-server-tuning.md`) — plumbing plus a little
   research on defaults.
10. Health Probe tuning (`06-probe-tuning.md`) — timeout is easy; per-backend
    expected status adds config-validation and Pool surface.
11. Monitoring server coverage in the integration suite — already unblocked;
    new scenarios on the existing `ExposeMonitoring` knob.

### Tier 3 — the engineering rocks (suggested-order positions 3 and 4)

12. Reload (`04-reload-via-sighup.md`, then `05-reload-file-watch.md`) — the
    last k8s engineering piece; born-red integration test + ADR.
13. Streaming (`01-fasthttp-streaming.md`, `02-http2-streaming.md`,
    `03-client-disconnect-aborts.md`, `05-streaming-adr-and-contract-docs.md`)
    — the long pole. If the tag date pressures, start it in parallel with
    reload; it must not be what gates the release.

### Tier 4 — distribution (near-free, prerequisites now met)

14. Official Docker release (`02-docker-image.md`) — bind flip landed in
    tier 1, logging already shipped.
15. Helm chart (`03-helm-chart.md`) — reuses the Docker release's GHCR login.
16. Example k8s manifests (`06-example-manifests.md`) — verify against the
    real published image; DNS knob from tier 1 available to set short.

### Tier 5 — docs, last by design

17. Docs restructure (`01-docs-tree-and-readme-slim.md` …
    `06-migration-notes.md`) — big in writing effort, zero technical risk;
    lands once the config surface has stopped moving.
