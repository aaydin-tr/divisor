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
- [ ] `--version` flag + version in the startup log — goreleaser ldflags.
  Ticket: `01-version-flag.md`.

## Kubernetes

Target: divisor runs first-class in k8s — a Deployment fronting pods (via a
headless Service or pod IPs), the L7 algorithms being the value-add over a
plain Service. The zero-alive-at-boot fix under "1.0 behavior fixes" is part
of this story: the LB must boot before its Backends.

Spec: `.scratch/kubernetes/spec.md` — tickets in `.scratch/kubernetes/issues/`
(the zero-alive-at-boot fix is ticket `01-zero-alive-at-boot.md` there).

- [ ] Backend DNS re-resolution — moved from Config surface; mandatory here
  (pod IPs churn constantly). fasthttp's TCPDialer caches resolved IPs for
  60s (`DNSCacheDuration` default), so a Backend that dies and is replaced —
  or an unrelated service reusing the freed IP — keeps being dialed at the
  stale IP for up to a minute. Surfaced by the integration suite while
  scenarios shared one docker network (an impostor container on a dead
  Backend's IP answered its traffic); the suite now isolates networks per
  Scenario, but the production exposure remains. Expose a configurable
  `DNSCacheDuration`; the k8s example manifests set it short. Ticket:
  `03-dns-cache-duration.md`.
- [ ] Readiness/liveness endpoints on the monitoring server — `/healthz`
  (liveness): 200 whenever the process runs. `/ready`: 200 once the
  listeners are bound, 503 during graceful shutdown. **Backend health never
  gates readiness** — zero Alive Backends is divisor *working* (answering
  503s, per zero-alive-at-boot); gating on it risks a bootstrap deadlock and
  hides divisor's own 503 signal. Unblocks the monitoring-coverage item
  under Suite / CI. Ticket: `02-probe-endpoints.md`.
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
- [ ] Default bind addresses — the client server's `host` default flips
  `localhost` → `0.0.0.0`: a load balancer's job is to accept outside
  traffic, and the localhost default makes the published Docker image
  silently unreachable. Monitoring keeps its `localhost` default (exposure
  is opt-in). Breaking change, pre-1.0. Ticket: `01-bind-default-flip.md`.
- [ ] `server.read_buffer_size` / `server.write_buffer_size` — fasthttp's 4KB
  default caps request header size; large cookies/JWTs hit "431/400 header too
  large" with no way to raise it. Ticket: `02-header-buffer-sizes.md`.
- [ ] `server.max_conns_per_ip`, `server.max_requests_per_conn` — basic
  self-protection knobs, currently unavailable. Ticket:
  `03-connection-caps.md`.
- [ ] `server.graceful_shutdown_timeout` — hardcoded to 30s in
  `main.go` (`performGracefulShutdown`). Ticket:
  `04-graceful-shutdown-timeout.md`.
- [ ] HTTP/2 server tuning (existing `TODO` at `main.go:158`) — pass a
  configured `http2.Server` (e.g. `max_concurrent_streams`, `idle_timeout`)
  instead of the zero value. Ticket: `05-http2-server-tuning.md`.
- [ ] Health Probe tuning — probe timeout and expected-status are hardcoded
  (GET, only 200 counts as Alive, client defaults in `pkg/http`); consider
  `health_checker_timeout` and per-backend expected status. Ticket:
  `06-probe-tuning.md`.
- [ ] TLS tuning — `tls_min_version` (and optionally cipher suites); currently
  whatever crypto/tls defaults to. Ticket: `07-tls-min-version.md`.

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
  upstream health. The machinery already exists: the Pool registers Down
  Backends at startup and the empty-rotation 503 path
  (`proxy.NoAliveBackends`) is shipped and tested.
  Implementation notes:
  - One decision in one place now: drop the `pool.AliveBackendCount() == 0 → nil`
    check in `core.NewBalancer` (every balancer's `Pick` already returns nil
    on an empty rotation, and the ring handles empty).
  - Keep `main.go`'s nil check as a defensive backstop — already exits
    non-zero since the Dockerfile/entrypoint exit-code item below shipped.
  - Unit test to flip: `TestNewBalancerIsNilWhenNothingIsAliveAtStartup` in
    `core` becomes "serves 503 until a Probe succeeds".
  - Do it born-red: integration scenario with every Backend `StartDown: true`
    → divisor boots, serves 503, then one Probe succeeds → traffic flows.
  - Trade-off accepted: a typo'd backend URL no longer fails fast at boot —
    divisor serves 503 instead of exiting; per-backend liveness stays visible
    in `/stats`, and this raises the value of the readiness-endpoint item
    under Kubernetes.
  Ticket: `.scratch/kubernetes/issues/01-zero-alive-at-boot.md` (specced
  with the Kubernetes feature).
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
  re-add the job when the next spec-red test lands (zero-alive-Backends-at-boot
  plans to be one).
- [ ] Go toolchain update to 1.27 (latest) — currently pinned to 1.25.3 in
  `go.mod`, `integration-test/go.mod`, all five workflow files under
  `.github/workflows/` (`go-version:`), and the Dockerfile builder stage
  (`golang:1.25-alpine`). Bump everywhere in one pass and run the full suite
  (`go test -race ./...` plus the integration suite).
- [ ] Monitoring server coverage (explicitly out of scope for the first suite);
  the readiness/liveness endpoints under Kubernetes are the prerequisite —
  they de-conflate divisor liveness from Backend health so `/metrics` can be
  tested with zero Alive Backends. Prerequisite ticket:
  `.scratch/kubernetes/issues/02-probe-endpoints.md`.

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
