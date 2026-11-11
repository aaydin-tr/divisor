# Features — post-1.0

Capabilities divisor does not have, from the post-1.0 review (2026-10-29) and
the items 1.0 deferred. Defects are in [bugs.md](bugs.md); the 1.0 record and
its carry-over items are in [TODO.md](TODO.md).

Priority reflects how much the feature changes what divisor is good for, not
its size. Each entry has the evidence for the gap, a sketch of the shape, and
the decisions it needs before a spec. Specs and tickets go to
`.scratch/<feature>/` as usual ([development/agents/issue-tracker.md](development/agents/issue-tracker.md)).

## Checklist

High
- [ ] [F1 — Retry on another Backend, and passive ejection](#f1)
- [ ] [F2 — TLS certificate reload without a restart](#f2)
- [ ] [F3 — X-Forwarded-Proto and X-Forwarded-Host](#f3)
- [ ] [F4 — Request metrics: status codes and latency](#f4)
- [x] [F5 — A load-aware least-response-time](#f5)

Medium
- [ ] [F6 — One Backend per DNS record](#f6)
- [ ] [F7 — Trusted proxies for the Client IP](#f7)
- [ ] [F8 — Affinity by header or cookie](#f8)
- [ ] [F9 — Helm chart values for real clusters](#f9)
- [ ] [F10 — WebSocket and connection upgrades](#f10)
- [ ] [F11 — Draining a Backend](#f11)
- [ ] [F12 — Slow start](#f12)

Low (or large and later)
- [ ] [F13 — Routing by host or path to several Pools](#f13)
- [ ] [F14 — Per-Backend proxy_timeout](#f14)
- [ ] [F15 — TLS to Backends](#f15)
- [ ] [F16 — Streaming on by default](#f16)

Docs
- [ ] [FD1 — A known-limitations page](#fd1)
- [ ] [FD2 — A hardening guide](#fd2)
- [ ] [FD3 — The docs site](#fd3)

---

## High

<a id="f1"></a>
### F1. Retry on another Backend, and passive ejection

- **Gap:** failure detection is Probe-only. `Serve` makes one Pick and one
  attempt ([core/balancer.go:104-113](core/balancer.go#L104-L113)), and a
  failed request never touches liveness. ADR 0003 states it: divisor "never
  retries the request on another Backend". With the defaults, a crashed
  Backend's share of requests gets 502 for up to `health_checker_time` +
  `health_checker_timeout` (35s), longer with several Down Backends while
  Probe rounds are sequential ([TODO.md](TODO.md#kubernetes), Concurrent
  Probe rounds). nginx (`proxy_next_upstream`), HAProxy (`redispatch`) and
  Envoy (retries, outlier detection) all retry connection failures elsewhere.
- **Shape:**
  - Retry on another Alive Backend when the attempt failed before any request
    byte reached the Backend (refused, dial timeout, no free connection): safe
    for any method. After the request was sent, only idempotent methods, and
    fasthttp already retries those on the same Backend
    (`max_idemponent_call_attempts`).
  - Every attempt shares one `proxy_timeout` budget, capped at N other
    Backends.
  - Passive ejection: N consecutive failed attempts mark a Backend Down until
    its next successful Probe, through the Pool so it stays the Balancer's one
    writer (`transitionsMu`).
- **Decisions:** ADR amending ADR 0003. `Pick` needs to exclude tried Backends
  (a `pool.Balancer` change). `preReq` sets `Host` and `custom_headers`
  (`$incremental` is per Backend) for the picked Backend, so each attempt
  re-prepares the request. Does `OnRequest` run once or per attempt? The
  Access log `backend` names the Backend that answered.
- **Docs:** shrink the 502 window in docs/getting-started.md
  ([bugs.md D2](bugs.md#d2)) once shipped.

<a id="f2"></a>
### F2. TLS certificate reload without a restart

- **Gap:** both stacks load the certificate pair once, in `ServeTLS`
  ([internal/server/server.go:64-65](internal/server/server.go#L64-L65),
  [:96-97](internal/server/server.go#L96-L97)). A renewed certificate at the
  same path changes no config key, so a Reload does nothing. With
  cert-manager or ACME (60-90 day certificates), divisor serves the old
  certificate until restarted, an expiry outage waiting to happen. No doc
  mentions it.
- **Shape:** `tls.Config.GetCertificate` returning an
  `atomic.Pointer[tls.Certificate]`; reload on `SIGHUP` and when the
  certificate or key file changes. A Kubernetes Secret volume swaps `..data`
  like a ConfigMap, so reuse `reload.ConfigFileWatch`'s symlink-resolution
  logic on the certificate's directory. A pair that fails to load keeps the
  running certificate and logs it, like a rejected Reload. fasthttp's
  `ServeTLS` loads `certFile` whenever it is non-empty, even with
  `GetCertificate` set, so serve through `tls.NewListener` instead.
- **Decisions:** is it a Reload (same log lines, same trigger), or its own
  event?

<a id="f3"></a>
### F3. X-Forwarded-Proto and X-Forwarded-Host

- **Gap:** divisor replaces `Host` with the Backend address
  ([internal/proxy/proxy.go:255](internal/proxy/proxy.go#L255)) and sets
  neither `X-Forwarded-Proto` nor `X-Forwarded-Host`; `custom_headers` cannot
  either, since it accepts only four variables
  ([pkg/config/config.go:63](pkg/config/config.go#L63)). A Backend behind
  divisor's TLS termination cannot tell the request was HTTPS or which host it
  was for: redirects and absolute URLs come out as `http://<backend-address>`,
  and secure-cookie logic misfires.
- **Shape:** set `X-Forwarded-Proto` (`https` on a TLS listener) and
  `X-Forwarded-Host` (the client's `Host`, read before `preReq` replaces it),
  replacing any client-sent value until [F7](#f7) lets a trusted proxy's
  values through. Optionally a `server.preserve_host` knob that forwards the
  client's `Host` unchanged.
- **Decisions:** always on, or opt-in? Also `Forwarded` (RFC 7239)?

<a id="f4"></a>
### F4. Request metrics: status codes and latency

- **Gap:** `/metrics` has gauges only
  ([internal/monitoring/prometheus.go:47-62](internal/monitoring/prometheus.go#L47-L62)):
  request counts, a lifetime average response time, liveness. There is no
  count by status, so a 5xx or 503 rate (the first thing to alert on) only
  comes from the Access log. Metric names have no namespace, and
  `backend_total_request_count` is a gauge that should be a counter.
- **Shape:** a `divisor_` prefix; `divisor_requests_total{backend, index,
  code_class}`; `divisor_request_duration_seconds` histogram per Backend;
  `divisor_no_alive_backend_responses_total` (the 503 path);
  `divisor_upstream_failures_total{reason}` (refused, timeout, reset).
- **Decisions:** renaming metrics breaks dashboards: pick the release, add a
  migration note. Measure the hot-path cost A/B, as H5 and the Access log
  were.

<a id="f5"></a>
### F5. A load-aware least-response-time

- **Gap:** the strict-minimum pick exiles a Backend after one failure
  ([bugs.md B2](bugs.md#b2)) and sends every concurrent request to one Backend
  ([bugs.md B3](bugs.md#b3)).
- **Shape:** power of two choices: pick two random Alive Backends and take the
  lower `moving average × (Pending requests + 1)`; score an unmeasured Backend
  as the pool's median, not as best. Drawn without replacement (Decisions), the
  worst Backend never wins a comparison: the Trial request keeps measuring it.
- **Decisions (settled 2026-11-03):** `least-response-time` changed in
  place. The two choices are drawn without replacement and the Trial request
  stays: drawn with, a Backend failing every request while its Probe passes
  would take 1/n² of requests (25% with two Backends); the Trial request
  already keeps B2 fixed. With nothing measured, Pending requests decide.
- **Fixes:** B3.

## Medium

<a id="f6"></a>
### F6. One Backend per DNS record

- **Gap:** a Backend is one `host:port`. To balance per pod, the docs require a
  StatefulSet behind a headless Service, one entry per pod
  ([docs/deployment/kubernetes.md:10-16](docs/deployment/kubernetes.md#L10-L16)).
  A Deployment behind a headless Service shows up as one Backend: one Probe,
  one liveness, one stats row, and least-connection cannot tell its pods
  apart.
- **Shape:** an opt-in per entry that resolves its host to every A/AAAA record
  and makes each address a Backend; re-resolved every Probe round, the
  difference applied through `Pool.ReplaceBackends` (the Reload path), each
  address keeping its Index while it stays resolved.
- **Decisions:** config shape; how ip-hash places resolved Backends (after
  [bugs.md B1](bugs.md#b1), by address).

<a id="f7"></a>
### F7. Trusted proxies for the Client IP

- **Gap:** deferred from 1.0 ([TODO.md](TODO.md#10-behavior-fixes-agreed-spec-tests-already-red),
  X-Forwarded-For semantics). The Client IP is always the TCP peer
  ([docs/configuration.md:156-161](docs/configuration.md#L156-L161)): behind a
  cloud load balancer or CDN, `$remote_addr`, the Access log `client_ip` and
  the ip-hash key are all that proxy.
- **Shape:** a CIDR list; when the TCP peer is trusted, the Client IP is the
  rightmost `X-Forwarded-For` entry not in the list. It feeds everything that
  reads the Client IP today.
- **Decisions:** whether [F3](#f3)'s headers pass through from a trusted peer.

<a id="f8"></a>
### F8. Affinity by header or cookie

- **Gap:** ip-hash keys on the Client IP, so every client behind one NAT lands
  on one Backend, and a client changing networks moves.
- **Shape:** hash on a configured request header, or a divisor-set cookie
  naming the Backend (sticky sessions), falling back to the hash when the
  cookie's Backend is Down.
- **Decisions:** a new Balancer, or ip-hash options? Depends on
  [bugs.md B1](bugs.md#b1) for consistency across replicas.

<a id="f9"></a>
### F9. Helm chart values for real clusters

- **Gap:** [charts/divisor/values.yaml](charts/divisor/values.yaml) exposes
  `replicaCount`, `image`, `terminationGracePeriodSeconds` and `config` only.
  There is no way to set resources, scheduling, annotations or the Service
  type, and Prometheus cannot reach `/metrics` through the Service (it exposes
  `http` only).
- **Shape:** `resources`, `nodeSelector`, `tolerations`, `affinity`,
  `podAnnotations`, `podLabels`, `service.type`/`annotations`, a monitoring
  port on the Service, an optional PodDisruptionBudget and ServiceMonitor,
  `image.pullPolicy`, `imagePullSecrets`.
- **Constraints:** `examples/kubernetes` must change with it
  (`TestHelmChartConfigMatchesTheKubernetesExample`), and the release dry-run
  runs `helm lint` and kubeconform `-strict` on the rendered chart.

<a id="f10"></a>
### F10. WebSocket and connection upgrades

- **Gap:** unsupported ([README.md:25-26](README.md#L25-L26)); `Upgrade` and
  `Connection` are stripped as hop-by-hop headers
  ([internal/proxy/proxy.go:41-51](internal/proxy/proxy.go#L41-L51)).
- **Shape:** on an `Upgrade` request, hijack the client connection and splice
  it to a connection dialed to the picked Backend; it counts as a Pending
  request until either side closes; an idle bound like
  `stream_idle_timeout`. HTTP/2 needs extended CONNECT (RFC 8441), separately.
- **Decisions:** ADR. What Middleware sees; graceful shutdown (the same open
  question as Streamed responses, [TODO.md](TODO.md#streaming)).

<a id="f11"></a>
### F11. Draining a Backend

- **Gap:** taking a Backend out of rotation for a deploy means removing its
  entry, which also drops its stats and Index.
- **Shape:** a reloadable `backends[].drain: true`: the Backend Leaves the
  rotation (in-flight requests finish), keeps its stats and stays Probed.
  It needs no API and no authentication, and Reload already carries it.
- **Decisions:** whether an HTTP endpoint on the Monitoring server is ever
  wanted too (it is unauthenticated today).

<a id="f12"></a>
### F12. Slow start

- **Gap:** a Backend that Rejoins, or that a Reload adds, takes its full share
  at once.
- **Shape:** ramp a new or Rejoined Backend's share over a configurable
  duration. Each Balancer implements the ramp in its own way.

## Low (or large and later)

<a id="f13"></a>
### F13. Routing by host or path to several Pools

- **Gap:** one process is one Pool; different hosts or paths need separate
  divisor instances.
- **Shape:** routes (host and/or path prefix) mapping to named Pools, each
  with its own Balancer and Probes. This changes the config's shape and
  Reload's scope: spec, grilling and an ADR first.

<a id="f14"></a>
### F14. Per-Backend proxy_timeout

- Deferred from 1.0 as additive ([TODO.md](TODO.md#config-surface-additions),
  `server.proxy_timeout`): a `backends[].proxy_timeout` override.

<a id="f15"></a>
### F15. TLS to Backends

- Deferred by ADR 0004 (plaintext-only Backends) as an additive feature:
  `https://` Backend URLs, CA and client certificate settings, and a Probe
  over TLS.

<a id="f16"></a>
### F16. Streaming on by default

- Planned for 1.1 ([TODO.md](TODO.md#streaming), opt-in knob): turn
  `server.streaming` on by default and delete the knob. Fix
  [bugs.md B4](bugs.md#b4) and [B5](bugs.md#b5) first, since today the two
  values of the knob answer failures differently.

## Docs

<a id="fd1"></a>
### FD1. A known-limitations page

- **Gap:** what divisor does not do is scattered or missing. Nowhere covers
  the 502 window before a Probe notices a Down Backend, the missing retry,
  certificate rotation, the ip-hash and least-response-time caveats, or the
  `Host` rewrite.
- **Shape:** one user-facing page in `docs/` covering each limitation and what
  to do meanwhile: Probe-only failure detection and no retry ([F1](#f1)),
  certificate rotation needs a restart ([F2](#f2)), `Host` rewritten and no
  `X-Forwarded-Proto` ([F3](#f3)), ip-hash behind NAT ([F8](#f8)), no
  WebSockets ([F10](#f10)), buffered uploads, one Pool per process
  ([F13](#f13)). Remove each line as its feature ships.
- **Docs:** move the ip-hash and least-response-time caveats here from
  examples/README.md ([bugs.md D1](bugs.md#d1)).

<a id="fd2"></a>
### FD2. A hardening guide

- **Gap:** production advice exists only implicitly, in
  `examples/production.config.yaml`.
- **Shape:** a `docs/` page covering client timeouts (the defaults are
  unlimited, [bugs.md B11](bugs.md#b11)), `max_conns_per_ip`,
  `tls_min_version`, keeping the Monitoring server private (it is
  unauthenticated), certificate rotation until [F2](#f2), and Middleware's
  trust model: Middleware code may import the whole standard library
  (`os/exec`, `net`, files), so a Middleware file deserves the same
  protection as the binary and the config.

<a id="fd3"></a>
### FD3. The docs site

- The mkdocs-material site, "the immediate post-1.0 follow-up"
  ([TODO.md](TODO.md#docs)).
