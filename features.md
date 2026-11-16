# Features — post-1.0

Capabilities divisor does not have, from the post-1.0 review (2026-10-29) and
the items 1.0 deferred. Defects are in [bugs.md](bugs.md); the 1.0 record and
its carry-over items are in [TODO.md](TODO.md).

Priority reflects how much the feature changes what divisor is good for, not
its size. Each entry has the evidence for the gap, a sketch of the shape, and
the decisions it needs before a spec. Specs and tickets go to
`.scratch/<feature>/` as usual ([development/agents/issue-tracker.md](development/agents/issue-tracker.md)).

An entry marked **Reviewed** has been through a critical review (is it needed,
what does it cost, what do other proxies do) and keeps that reasoning, the
parts that were cut included, for the grilling session before its spec.

## Checklist

High
- [ ] [F1a — Retry on another Backend when the connection fails](#f1a)
- [ ] [F1c — A shorter default Probe interval](#f1c)
- [ ] [F2 — TLS certificate reload without a restart](#f2)
- [ ] [F3 — Forward the client's Host, X-Forwarded-Proto and X-Forwarded-Host](#f3)
- [ ] [F4 — Request metrics: status codes and latency](#f4)
- [x] [F5 — A load-aware least-response-time](#f5)
- [ ] [F6 — One Backend per DNS record](#f6)
- [ ] [F7 — The Client IP behind a trusted proxy](#f7)
- [ ] [F10 — WebSockets](#f10)

Medium
- [ ] [F1b — A failed attempt triggers an immediate Probe](#f1b)
- [ ] [F8a — ip-hash on a request header](#f8a)
- [ ] [F9 — Helm chart values for real clusters](#f9)
- [ ] [F16 — Streaming on by default](#f16)
- [ ] [F19 — Large responses are held whole in memory](#f19)

Low (or large and later)
- [ ] [F8b — Sticky sessions by a divisor-set cookie](#f8b)
- [ ] [F11 — Draining a Backend](#f11)
- [ ] [F12 — Slow start](#f12)
- [ ] [F13 — Routing by host or path to several Pools](#f13)
- [ ] [F15 — TLS to Backends](#f15)
- [ ] [F17 — PROXY protocol on the listener](#f17)
- [ ] [F18 — gRPC, and HTTP/2 to Backends](#f18)

Not planned (reviewed and declined)
- [F14 — Per-Backend proxy_timeout](#f14): no use case

Docs
- [ ] [FD1 — A known-limitations page](#fd1)
- [ ] [FD2 — A hardening guide](#fd2)
- [ ] [FD3 — The docs site](#fd3)
- [ ] [FD4 — Deploying your Backends without errors](#fd4)

## Order of work

Put together from the reviews (2026-11-12 to 2026-11-17). Priority says what
matters; this says what comes first, and why.

1. **Docs that help today:** [FD1](#fd1), then [FD4](#fd4) and [FD2](#fd2).
2. **Seeing and shrinking failures:** Concurrent Probe rounds
   ([TODO.md](TODO.md#kubernetes)), then [F1c](#f1c), which depends on them.
   [F4](#f4), at least its failure counters, before [F1a](#f1a), so the
   retry's effect can be measured. Then F1a.
3. **Small and independent, at any time:** [F2](#f2).
4. **One release together:** [F3](#f3) and [F7](#f7), which share the
   `trusted_proxies` list. Then [F8a](#f8a), which builds on F7's Client IP.
5. **Kubernetes:** [F6](#f6) after F1a, with [F9](#f9) in the same change to
   the chart.
6. **Streams:** end open streams when the drain begins
   ([TODO.md](TODO.md#streaming)), then [F19](#f19) together with
   [F16](#f16) (the flip alone triples the memory of a large response), then
   [F10](#f10). F19's buffer sizing is a one-line fix that can go at once.
7. **After measuring:** [F1b](#f1b), only if F1a and F1c leave a gap worth
   it.
8. **On a real request:** [F8b](#f8b) with [F11](#f11), [F12](#f12),
   [F15](#f15) with [F18](#f18), [F17](#f17). [F13](#f13) is revisited when
   the High list is done. [FD3](#fd3) before a public announcement.

---

## High

<a id="f1"></a>
### F1. A failed Backend: retry and faster detection

Reviewed 2026-11-12. The entry was "Retry on another Backend, and passive
ejection": three features in one. It is now [F1a](#f1a) (the retry, High),
[F1c](#f1c) (a shorter default Probe interval: High, nearly free, so first)
and [F1b](#f1b) (faster detection, Medium). Three parts of the old shape were
cut. This section holds what the three entries share.

- **Gap:** failure detection is Probe-only. `Serve` makes one Pick and one
  attempt ([core/balancer.go:104-113](core/balancer.go#L104-L113)), and a
  failed request never touches liveness. ADR 0003 states it: divisor "never
  retries the request on another Backend". With the defaults, a crashed
  Backend's share of requests gets 502 for up to `health_checker_time` +
  `health_checker_timeout` (35s): a quarter of all requests with four
  Backends. Longer with several Down Backends while Probe rounds are
  sequential ([TODO.md](TODO.md#kubernetes), Concurrent Probe rounds).
- **Why it matters:**
  - A load balancer has two jobs: spread load, and hide a Backend failure
    from clients. divisor does the second only once a Probe has noticed.
  - It is not only crashes. Every Backend deploy or restart is a window of
    502s, so the errors are routine, not rare.
  - It is the first test an evaluator runs: start three Backends, kill one,
    watch. nginx with its defaults keeps answering; divisor fails a third of
    the requests for up to 35s.
  - Clients do not cover it: a browser does not retry a 502.
- **Other proxies** (from their documentation as recalled on 2026-11-12, not
  re-read: confirm before an ADR cites them):

  | Proxy | Retry on a connection failure | Passive ejection |
  |---|---|---|
  | nginx | On by default: `proxy_next_upstream error timeout`. A non-idempotent request is not passed on once it was sent (`non_idempotent` lifts that) | On by default: `max_fails=1 fail_timeout=10s`. It is the only health check in open-source nginx (active checks are NGINX Plus). Ignored for a group of one server |
  | HAProxy | `retries 3` by default, on connection failures (`retry-on conn-failure`); `option redispatch` sends the retry to another server | Opt-in: `observe` with `on-error`, whose default mode `fail-check` counts the error as a failed health check instead of marking the server down |
  | Caddy | Opt-in (`lb_try_duration`, `lb_retries`). A failed dial is always retryable; once connected, only requests matching `lb_retry_match` (GET by default) | Opt-in (`fail_duration`, `max_fails`) |
  | Traefik | Opt-in Retry middleware (`attempts`) | Not looked up |
  | Envoy | Opt-in retry policy (`retry_on: connect-failure`) | Opt-in outlier detection, guarded by `max_ejection_percent` (default 10%) and an ejection time that grows with each ejection |

  Every upstream Contender of the Release benchmark (Caddy, nginx, Traefik)
  can retry a connection failure on another server; nginx does by default.
- **Verified in the code** (2026-11-12, fasthttp v1.78.0):
  - fasthttp never retries a failed connection: `transport.RoundTrip`
    returns `retry=false` when `AcquireConn` fails, so refused, dial timeout
    and no free connection reach the client as a 502 after one attempt. It is
    also the one point where no request byte can have reached the Backend,
    which makes a retry safe for any method, POST included.
  - Once the request is written, fasthttp retries GET, HEAD, PUT and QUERY
    (`isIdempotent`) on the same Backend, up to
    `max_idemponent_call_attempts` (default 5), inside the one
    `proxy_timeout` deadline. With `server.streaming` on, a failure while
    reading the response body is not retried (ADR 0007, [F16](#f16)).
  - A dial is bounded at 3s (ADR 0003). A refused connection fails at once; a
    black-holed address (a dead node, often a deleted pod's IP) costs the
    whole 3s.
  - One failed Probe marks a Backend Down and one success Rejoins it: there
    is no threshold (`Pool.updateLiveness`). A round Probes one Backend after
    another under `transitionsMu`.
  - The Probe has its own client ([pkg/http/http.go](pkg/http/http.go)), so
    it passes while the Backend's `HostClient` has no free connection.
  - With `server.streaming` on, a connection failure returns from
    `forwardAllowingStream` before any response exists, so a retry needs no
    streaming case.
- **Cut from the old shape, and why:**
  - *Retry on another Backend after the request was sent (idempotent
    methods).* Every attempt shares one `proxy_timeout` budget, so a 504 can
    never be retried: the budget is spent. What is left is a connection reset
    mid-request, which is rare and which fasthttp already retries on the same
    Backend; a retry elsewhere risks running a PUT twice. Reopen only with
    numbers ([F4](#f4)'s `divisor_upstream_failures_total{reason}`).
  - *"No free connection" as a retryable failure.* It is saturation, not
    failure. With the defaults (`max_conn: 512`, `max_conn_timeout: 30s`) it
    arrives after a 30s wait, half the `proxy_timeout` budget, and a retry
    moves the overload to the next Backend. A separate decision if it is ever
    wanted.
  - *Passive ejection: N consecutive failed attempts mark a Backend Down.*
    Replaced by [F1b](#f1b), because:
    - With F1a in place it prevents no errors. It only saves latency, and
      only for a black-holed Backend (3s per attempt): retrying past a
      refused connection costs under a millisecond.
    - It makes request failures a second judge of liveness beside the Probe.
      A Backend whose Probe passes while requests to it fail would be ejected
      and Rejoined every round.
    - Any network blip on divisor's side could mark healthy Backends Down for
      a whole Probe interval. Envoy needs `max_ejection_percent` and growing
      ejection times against that; nginx ignores `max_fails` for a lone
      server.
    - N and its time window are two more knobs to explain.

<a id="f1a"></a>
### F1a. Retry on another Backend when the connection fails

Split from [F1](#f1), which has the evidence and what was cut.

- **Gain:** a crashed, restarting or deploying Backend costs no client an
  error while another Backend is Alive. The 502 window becomes, at worst,
  added latency: nothing for a refused connection, the 3s dial timeout for a
  black-holed one.
- **Shape:**
  - When an attempt fails before the connection to the Backend exists
    (refused, reset during the dial, dial timeout), Pick another Alive
    Backend and try again, whatever the method.
  - On by default, as in nginx: the case is safe for every method.
  - At most 1-2 other Backends, every attempt inside the one `proxy_timeout`
    budget. When they all fail, the answer is today's 502.
  - A failed attempt still feeds the failed Backend's Score
    (`recordFailure`), as today, so least-response-time steers away before
    the Probe notices.
- **Cost:**
  - The retry loop is small and runs only after a failure: the success path
    gains one branch. Measure it A/B anyway, as H5 and the Access log were.
  - The work is in `ReverseProxyHandler`
    ([internal/proxy/proxy.go:108-194](internal/proxy/proxy.go#L108-L194)):
    one per-Backend method owns the whole request (sequence number, Access
    log entry, `preReq`, `OnRequest`, the attempt, `OnResponse`, the error
    answer). `preReq` rewrites `ctx.Request` in place, before `OnRequest`: it
    sets `Host`, appends to `X-Forwarded-For` and sets `custom_headers`.
    Handing the request to a second Backend's handler would append
    `X-Forwarded-For` twice, run `OnRequest` twice and write two Access log
    lines. So "the client request, once" has to be split from "the attempt,
    per Backend", in the file that also holds Streamed responses, the Trial
    request and trailers.
  - `Pick` has to exclude tried Backends: a `pool.Balancer` change in all six
    Balancers. Calling `Pick` again and skipping a repeat is not enough,
    because ip-hash returns the same Backend every time. ip-hash can walk the
    ring to the next position an untried Backend owns, which is the Backend
    that client maps to anyway once the failed one is Down.
- **Decisions:**
  - An ADR amending ADR 0003.
  - On by default: confirm. Is there a knob (off, or the cap), and its name?
    A new key is restart-only by default.
  - The cap: 1 or 2 other Backends.
  - Does `OnRequest` run once or per attempt? Once fits what Middleware is
    for (the client's request), but today it runs after `preReq`, so it sees
    `Host` already set to the picked Backend. What does it see when the
    Backend can change?
  - What is prepared once (`X-Forwarded-For`, hop-by-hop headers, trailers)
    and what per attempt (`Host`, `custom_headers`: `$incremental` is the
    Backend's own sequence number).
  - Does a failed attempt count in the failed Backend's request count? What
    happens to a claimed Trial request whose attempt fails to connect?
  - The Access log `backend` names the Backend that answered. Does the line
    also say it was retried (an attempt count, the Backends that failed)?
    [F4](#f4) should count failed attempts and retries.
  - What "another Backend" means per Balancer: the ring's next owner for
    ip-hash; for the others, their usual Pick over the untried Backends.
- **Docs:** shrink the 502 window in docs/getting-started.md
  ([bugs.md D2](bugs.md#d2)) once shipped, and update the [FD1](#fd1) line.

<a id="f1c"></a>
### F1c. A shorter default Probe interval

Split from [F1](#f1). Do it first: it is nearly free.

- **Gap:** `health_checker_time` defaults to 30s
  ([pkg/config/config.go:79](pkg/config/config.go#L79)), which with the 5s
  `health_checker_timeout` is the 35s window. HAProxy's default `inter` is
  2s, NGINX Plus's `health_check` interval 5s, a Kubernetes readiness probe's
  `periodSeconds` 10s; Caddy and Traefik default to 30s (recalled, as in F1).
- **Shape:** lower the default to 5-10s. A Probe is one GET per Backend per
  round, so it costs nothing that matters. A one-line change that shrinks the
  502 window 3-6 times before F1a exists; after F1a it shrinks the time a
  black-holed Backend costs 3s per attempt.
- **Depends on:** Concurrent Probe rounds ([TODO.md](TODO.md#kubernetes)).
  Rounds are sequential, so one black-holed Backend holds a round for up to
  `health_checker_timeout` (5s): at a 5s interval a single one fills it, and
  every Backend behind it is Probed late.
- **Decisions:**
  - The value: 5s or 10s.
  - Does the `health_checker_timeout` default shrink with it, and must it
    stay below the interval?
  - One failed Probe marks a Backend Down. At a shorter interval one slow
    Probe answer takes a Backend out more often, though it Rejoins in
    seconds, not in 30s. HAProxy (`fall 3 rise 2`) and Kubernetes
    (`failureThreshold: 3`) ask for consecutive failures: does divisor need
    that? (Raised while writing this entry, not in the review.)
  - It changes behavior for every config that does not set the key: a
    release note.

<a id="f2"></a>
### F2. TLS certificate reload without a restart

Reviewed 2026-11-13: kept at High, with a smaller shape. The old shape's
fsnotify watch and its `tls.NewListener` detour are cut; expiry visibility
and the half-written pair are added.

- **Gap:** both stacks load the certificate pair once, in `ServeTLS`
  ([internal/server/server.go:66-69](internal/server/server.go#L66-L69),
  [:115-116](internal/server/server.go#L115-L116)). A renewed certificate at
  the same path changes no config key, so a Reload does nothing. With
  cert-manager or ACME, divisor serves the old certificate until restarted.
  No doc mentions it.
- **Why it matters:**
  - The failure is silent and looks like success.
    [docs/deployment/systemd.md](docs/deployment/systemd.md) says
    `systemctl reload divisor` sends `SIGHUP`. An operator who puts that in
    certbot's deploy hook, as for nginx, gets a Reload that finds no changed
    key: divisor keeps the old certificate, and nothing fails until it
    expires weeks later.
  - The workaround, a restart on every renewal, is a short full outage for a
    single divisor: the listener is closed while the old process drains.
    nginx's reload has no such gap.
  - Everyone on HTTP/2 has a certificate at divisor: HTTP/2 requires TLS.
  - Priority: it does not change what divisor is good for. It stays High
    because it is one or two days of work, low risk, and the failure is an
    outage.
- **Other proxies** (recalled on 2026-11-13, not re-read: confirm before an
  ADR cites them):
  - nginx and HAProxy do not watch certificate files. A reload (`SIGHUP`,
    `systemctl reload`) re-reads them, and certbot's `--deploy-hook` exists
    to send it. HAProxy can also swap a certificate over its Runtime API.
    This is the baseline.
  - Caddy and Traefik run ACME themselves.
  - Envoy takes certificates over SDS, which can watch files.
  - Lifetimes are shrinking: the CA/Browser Forum maximum is 200 days since
    March 2026, 100 days from March 2027 and 47 days from March 2029. Let's
    Encrypt issues 90-day certificates and offers 6-day ones. certbot and
    cert-manager renew a 90-day certificate about 30 days before it expires.
- **Verified in the code** (2026-11-13, fasthttp v1.78.0):
  - fasthttp's `ServeTLS` loads the files when `certFile` or `keyFile` is
    non-empty, or when the config has neither `Certificates` nor
    `GetCertificate`. With `GetCertificate` set and both paths empty it loads
    nothing, so `ServeTLS(ln, "", "")` serves from `GetCertificate`.
    net/http's `ServeTLS` documents the same rule.
  - `tlsConfig` returns nil when `tls_min_version` is unset
    ([internal/server/server.go:128-134](internal/server/server.go#L128-L134)):
    with `GetCertificate` it always has to build a config.
  - The chart and docs/deployment/kubernetes.md have no way to mount a
    certificate today.
  - The config file watch raises no event for a single-file Docker bind mount
    or for an edit through a symlink into another directory (ADR 0006).
- **Shape:**
  - `tls.Config.GetCertificate` returning an
    `atomic.Pointer[tls.Certificate]`: one atomic load per handshake. Both
    stacks call `ServeTLS(ln, "", "")`.
  - The pair is re-read on `SIGHUP` and by a poll once a minute, and swapped
    in when the files changed and load. The poll stops with graceful
    shutdown.
  - A pair that fails to load keeps the running certificate and logs it, like
    a rejected Reload. The next poll tries again, which covers a renewal
    caught half-written (new certificate, old key); with `SIGHUP` alone
    divisor would stay on the old pair.
  - Every load logs the certificate's expiry (`NotAfter`), and `/metrics`
    exports it, so expiry can be alerted on. Without that, a reload that
    fails quietly is the same trap moved one step.
  - Only the files' content reloads: a changed `cert_file` or `key_file` path
    stays restart-only.
- **Cut from the old shape, and why:**
  - *The fsnotify watch reusing `reload.ConfigFileWatch`'s symlink
    resolution.* A certificate renews about 30 days before it expires, so a
    minute's delay costs nothing, and a poll needs no symlink logic. A poll
    also works where the watch does not (Verified). certbot's `live/`
    directory is symlinks into `archive/`.
  - *The Kubernetes Secret volume as the design driver.* No chart value or
    doc can mount one today, and in a cluster TLS usually ends at the Ingress
    or Gateway. The poll covers a Secret volume anyway.
  - *Serving through `tls.NewListener`.* Not needed: empty paths are enough
    (Verified).
- **Decisions:**
  - Is it a Reload or its own event? Recommended: `SIGHUP` re-reads the
    certificate too, since that is what `systemctl reload` means to an nginx
    operator, but as its own event with its own log line, outside
    `PlanReload`: the classifier compares config keys, and none changed.
  - The poll interval, and whether it is a knob. Recommended: fixed at one
    minute.
  - How the poll sees a change: modification time and size, or re-reading
    and comparing the bytes.
  - The metric's name, and whether it waits for [F4](#f4)'s `divisor_`
    prefix.
  - Does a failed load show on `/metrics` too, or only in the log?
- **Docs:** docs/deployment/systemd.md gets the certbot deploy hook
  (`systemctl reload divisor`); the certificate rotation lines of
  [FD1](#fd1) and [FD2](#fd2) go once shipped.

<a id="f3"></a>
### F3. Forward the client's Host, X-Forwarded-Proto and X-Forwarded-Host

Reviewed 2026-11-13: kept at High and widened. Forwarding the client's `Host`
becomes the main part and the default, and the `trusted_proxies` list moves
here from [F7](#f7) (both decided). `Forwarded` (RFC 7239) is cut.

- **Gap:** divisor replaces `Host` with the Backend address
  ([internal/proxy/proxy.go:278](internal/proxy/proxy.go#L278)) and sets
  neither `X-Forwarded-Proto` nor `X-Forwarded-Host`. `custom_headers` cannot
  either: it accepts only its four variables
  ([pkg/config/config.go:327-331](pkg/config/config.go#L327-L331)). A
  client-sent `X-Forwarded-Proto` or `X-Forwarded-Host` passes through
  untouched.
- **Why it matters:**
  - The scheme. A Backend behind divisor's TLS cannot tell the request was
    HTTPS. An app that forces HTTPS (Rails `force_ssl`, Django
    `SECURE_SSL_REDIRECT`, WordPress) answers every request with a redirect
    to HTTPS: a loop, and the site does not load. Secure-cookie logic
    misfires.
  - The hostname. The Backend receives `Host: <backend address>`: the wrong
    virtual host, redirects and absolute URLs that point at the Backend
    address, host allow-lists (Django `ALLOWED_HOSTS`) that reject the
    request, no routing by subdomain.
  - Spoofing at the edge. When divisor faces clients, a client can send
    `X-Forwarded-Host: evil.example` or `X-Forwarded-Proto: https` and the
    Backend may believe it.
  - No supported way around it. `custom_headers` cannot set these. A
    Middleware can (Verified), but that is Go code written against a detail
    no doc states.
- **Other proxies** (recalled on 2026-11-13, not re-read: confirm before an
  ADR cites them):

  | Proxy | Client's `Host` | `X-Forwarded-Proto` / `-Host` |
  |---|---|---|
  | nginx | Replaced by default (`$proxy_host`); nearly every config adds `proxy_set_header Host $host` | Not set by default; set by hand in nearly every config |
  | HAProxy | Forwarded unchanged | Opt-in (`http-request set-header`) |
  | Caddy | Forwarded unchanged | Set by default; client-sent values dropped unless the peer is in `trusted_proxies` |
  | Traefik | Forwarded unchanged (`passHostHeader`) | Set by default; client-sent values kept only from `forwardedHeaders.trustedIPs` |
  | Envoy | Forwarded unchanged | `x-forwarded-proto` set by default |
  | AWS ALB | Forwarded unchanged | Adds `X-Forwarded-Proto` and `X-Forwarded-Port` |

  divisor's rewrite is nginx's default, the one default every nginx guide
  overrides. nginx lets you; divisor has no knob. Caddy and Traefik treat
  "set these headers" and "whom to trust" as one feature.
- **Verified** (2026-11-13, fasthttp v1.78.0; the first two by running a
  small program against fasthttp):
  - `preReq`'s `req.SetHostBytes` changes the request URI's host only.
    `req.Header.Host()` and `Header.Peek("Host")` still return the client's
    `Host` afterwards. So the value for `X-Forwarded-Host` is at hand in
    `preReq`, and a Middleware's `OnRequest` can read it today, although
    [docs/middleware.md:121-124](docs/middleware.md#L121-L124) says only
    "`Host` set to the Backend's address".
  - `Request.Write` puts the URI's host on the wire unless
    `Request.UseHostHeader` is true. With it true the client's `Host` goes
    out, and the connection still goes to the Backend (`HostClient` dials its
    own `Addr`). Forwarding `Host` is that one flag.
  - On the HTTP/2 stack the adapter builds the `RequestCtx` with
    `ctx.Init(..., nil)`
    ([internal/proxy/nethttp_adapter.go:58-59](internal/proxy/nethttp_adapter.go#L58-L59)):
    there is no TLS connection behind it, so `ctx.IsTLS()` should be false
    there although every such connection is TLS (read, not run).
  - A test pins the rewrite
    ([internal/proxy/proxy_test.go:1529-1549](internal/proxy/proxy_test.go#L1529-L1549)).
- **Decided (2026-11-13):**
  - The client's `Host` is forwarded by default. Changing 1.0 behavior is
    accepted when the docs say so clearly.
  - The `trusted_proxies` list ships with this feature, not with F7.
- **Shape:**
  - Forward the client's `Host` to the Backend. A knob brings back the
    rewrite to the Backend address, for a Backend that routes by its own
    name.
  - Set `X-Forwarded-Proto` (`https` when divisor serves TLS, else `http`)
    and `X-Forwarded-Host` (the client's `Host`), replacing whatever the
    client sent.
  - `server.trusted_proxies`, a CIDR list, empty by default (divisor is the
    edge). When the TCP peer is in it, its `X-Forwarded-Proto` and
    `X-Forwarded-Host` pass through unchanged: the proxy in front terminated
    TLS and knows the real values.
  - TLS is per process (a certificate is configured or it is not), so the
    scheme is a startup constant, not read from `ctx.IsTLS()`.
- **Cut, and why:**
  - *`Forwarded` (RFC 7239).* Frameworks read `X-Forwarded-*`, and sending
    both gives a Backend two sources that can disagree. Reopen on a real
    request.
  - *`X-Forwarded-Host` alone, the rewrite kept as the default.* Every
    framework has to be told to trust it (Django `USE_X_FORWARDED_HOST`,
    Express `trust proxy`), where a forwarded `Host` needs nothing. The
    rewrite only suits a Backend that routes by its own name, rare for
    plaintext Backends on a private network.
  - *Always on with no trust list.* Behind a load balancer that terminates
    TLS, divisor's own listener is plain HTTP, so it would write
    `X-Forwarded-Proto: http` over the load balancer's correct `https`. That
    setup works today, because the header passes through; it would become a
    redirect loop.
- **Cost:** small code: one flag and two header writes per request in
  `preReq`, and a CIDR check only when the list is set. Measure the hot path
  A/B. The work is the decisions and the docs.
- **Decisions (open):**
  - The name of the knob for the old rewrite, and whether it is per Backend.
  - A trusted peer that sends no `X-Forwarded-Proto` or `X-Forwarded-Host`:
    divisor sets its own values?
  - `X-Forwarded-Port`: not planned, the forwarded `Host` carries the port.
    Confirm.
  - What `OnRequest` sees: `ctx.Request.Host()` returns the Backend address
    today. Does that stay? ([F1a](#f1a) asks the same when the Backend can
    change.)
  - Is `trusted_proxies` reloadable? A new key is restart-only by default.
- **Docs** (the condition for changing 1.0 behavior): docs/configuration.md
  (what divisor forwards), docs/middleware.md:121-124 (what `OnRequest`
  sees), the release notes, and the [FD1](#fd1) line goes.

<a id="f4"></a>
### F4. Request metrics: status codes and latency

Reviewed 2026-11-16: kept at High, with the design the old entry left out.
The exact status code replaces `code_class`, the failure reasons shrink to
three, divisor's copies of the process metrics go, and the failure counters
ship before [F1a](#f1a).

- **Gap:** `/metrics` has gauges only
  ([internal/monitoring/prometheus.go:17-63](internal/monitoring/prometheus.go#L17-L63)):
  request counts, a lifetime average response time, liveness. No status code
  appears anywhere. Metric names have no namespace, and
  `backend_total_request_count` is a gauge that should be a counter.
- **Why it matters:**
  - `/metrics` cannot answer "is divisor returning errors right now?". The
    only source is the Access log, which is off by default and needs a log
    pipeline to become a number.
  - The latency metric is a lifetime average. After a day of traffic it
    barely moves, and an average hides the slow tail: no latency objective
    can be built on it.
  - No error-rate alert is a blocker for production for most teams, and
    `/metrics` is one of the first things an evaluator opens.
- **Other proxies** (recalled on 2026-11-13, not re-read: confirm before an
  ADR cites them):
  - Rate, errors and duration are the standard three. Traefik, Caddy and
    Envoy export requests by status code and a duration histogram. HAProxy
    exports responses by code class, and averages. Open-source nginx exports
    almost nothing (`stub_status`), a known weakness people patch with
    exporters and log parsing.
  - Who made the error is what operators want most from a load balancer. AWS
    ALB has two metrics for it, `HTTPCode_ELB_5XX` and
    `HTTPCode_Target_5XX`; Envoy has response flags. A 502 the Backend
    returned and a 502 divisor made because the Backend was unreachable call
    for different reactions.
- **Verified in the code** (2026-11-16, client_golang as pinned in go.mod):
  - A poller copies every metric every 5s (`prometheusUpdateInterval`,
    [internal/monitoring/monitoring.go:26](internal/monitoring/monitoring.go#L26))
    from the Pool's stats and gopsutil. Nothing touches Prometheus during a
    request: `ProxyClient` keeps its own atomic counters.
  - `/metrics` serves `promhttp.Handler()`, the default registry, in which
    client_golang registers its process and Go collectors
    (`prometheus/registry.go:61-62`). So the standard `process_*` and `go_*`
    series stand next to divisor's `process_memory_mb`,
    `process_memory_percent`, `process_cpu_percent` and `total_goroutine`
    (read in the library source; `/metrics` was not scraped).
  - client_golang's default histogram buckets start at 5ms (`DefBuckets`).
    `ProxyClient` keeps microseconds because sub-millisecond Backends exist
    ([internal/proxy/proxy.go:229-232](internal/proxy/proxy.go#L229-L232)),
    so a fast Backend would fall entirely into the first bucket.
  - `backend_average_response_time` is in milliseconds
    ([docs/observability.md:142](docs/observability.md#L142)); the Prometheus
    convention is seconds.
  - The dashboard reads `/stats`, its own JSON, so a rename touches only
    `/metrics`. The names are asserted in
    integration-test/monitoring_test.go and listed in docs/observability.md
    and docs/migration-1.0.md.
  - The answers divisor makes itself: 503 (`NoAliveBackends`), 502 and 504
    (`serverError`), 500 (`middlewareError`), a Middleware Short-circuit, and
    fasthttp's `ErrorHandler` (413, 431, 408, 400), which never reaches the
    proxy handler.
- **Shape:**
  - `divisor_requests_total{address, index, code}`: a counter with the exact
    status code.
  - `divisor_request_duration_seconds{address, index}`: one histogram per
    Backend, no other label, buckets starting below 1ms.
  - A counter of the errors divisor made itself: no Alive Backend (the 503
    path), and upstream failures by reason: `connect`, `timeout`, `other`.
  - The series that stay get the `divisor_` prefix and base units (seconds,
    bytes). divisor's copies of the process and Go metrics are dropped, not
    prefixed.
  - No Prometheus call on the request path: atomic counters in `ProxyClient`
    (it has them already) and a fixed array of histogram buckets, read by a
    collector at scrape time.
  - One rename, in one release, with an old-to-new table in the docs. No
    period of exporting both names (the rule on 1.0 behavior, decided in
    [F3](#f3)).
- **Changed from the old shape, and why:**
  - *`code_class` became the exact `code`.* ADR 0003 separates 502
    (unreachable), 503 (no Alive Backend) and 504 (hanging). `5xx` merges
    them again, and the old shape needed two extra counters to get them back.
    Traefik and Caddy label the exact code. A Backend answers with a handful
    of codes, so the series count stays small.
  - *The reasons "refused, timeout, reset" became `connect`, `timeout`,
    `other`.* The fine ones need fragile error matching. `connect` is exactly
    F1a's retry condition, so the two features share one classifier.
  - *Scrape-time collection, not `WithLabelValues` per request.* The obvious
    implementation adds a hash and a lock to the path the Release benchmark
    measures. The poller pattern already keeps Prometheus off that path.
  - *The duplicates are dropped, not renamed.* The standard collectors
    already export memory, CPU and goroutines in standard units.
- **Order:** the failure counters ship before F1a, so F1a's effect can be
  measured. [F1](#f1)'s cut parts reopen only with these numbers.
- **Cost:** two to four days with the A/B run. Low risk in behavior. The risk
  is in the design: metric names are an API, and a second rename is another
  break. Design them once.
- **Decisions:**
  - What the histogram measures: the Backend's answer time, as the Score
    does, or the whole request as the client saw it? A Streamed response:
    time to the first response byte, as today's average?
  - Do failed requests go into the histogram?
  - The bucket boundaries.
  - Answers with no Backend involved (the 503 path, `ErrorHandler`'s 4xx):
    their own counter, or `divisor_requests_total` with an empty Backend?
  - A Middleware Short-circuit or Middleware 500: counted under the picked
    Backend?
  - With [F1a](#f1a), a retried request: one count per attempt, or per client
    request?
  - A scrape then reads the Pool's stats. Graceful shutdown must keep
    stopping the Monitoring server before the Pool, as it does today.
  - Does the poller stay for `/stats` and the dashboard?
  - [F2](#f2)'s certificate expiry metric takes the same prefix.
  - Measure the hot-path cost A/B, as H5 and the Access log were.
  - Leave room for a `pool` label ([F13](#f13)'s hedge): a label added later
    is not a rename.
- **Docs:** docs/observability.md (the table, the example queries, the note
  that `backend_total_request_count` is a gauge) and an old-to-new table in
  the release notes. integration-test/monitoring_test.go asserts the names.

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

<a id="f6"></a>
### F6. One Backend per DNS record

Reviewed 2026-11-16: moved from Medium to High, ordered after [F1a](#f1a).
The shape gains three rules the old entry lacked (a failed lookup, where the
lookup runs, two levels of Backend). SRV records are cut.

- **Gap:** a Backend is one `host:port`. To balance per pod, the docs require
  a StatefulSet behind a headless Service, one entry per pod
  ([docs/deployment/kubernetes.md:10-23](docs/deployment/kubernetes.md#L10-L23)).
  A Deployment behind a headless Service shows up as one Backend: one Probe,
  one liveness, one stats row, and least-connection cannot tell its pods
  apart.
- **Why it matters:**
  - It decides whether divisor is useful on Kubernetes at all. A normal
    Deployment has no stable pod names, so its pods cannot be listed.
  - Autoscaling cannot work: the pod set changes, the config does not.
  - The alternative, pointing divisor at the Service, lets Kubernetes
    balance: divisor's Balancers then choose nothing.
  - divisor ships a Helm chart, yet cannot balance the workload type most
    apps on Kubernetes use.
  - Not only Kubernetes: Docker Compose `--scale` and Consul publish several
    records per name too.
  - Priority: by this file's rule (how much it changes what divisor is good
    for) it was the biggest item in Medium.
- **What happens today:** a hostname with several records is not one address
  (Verified). fasthttp's dialer resolves all of them, caches them for
  `dns_cache_duration` and opens connections round-robin across them. So one
  Backend already spreads its connections over several pods, with one Probe
  (to whichever address the Probe client's own dialer takes next), one
  liveness and one stats row for all of them. Hidden, uneven, and not
  observable.
- **Other proxies** (recalled on 2026-11-16, not re-read: confirm before an
  ADR cites them):
  - HAProxy: `server-template` with a `resolvers` section, which also has
    hold times before a vanished record is dropped (`hold nx`,
    `hold obsolete`).
  - Envoy: `STRICT_DNS` clusters. Each resolved address is a host,
    re-resolved every few seconds.
  - Caddy: dynamic upstreams from A or SRV records.
  - nginx: `resolve` on upstream servers. NGINX Plus only for years, open
    source since 1.27.3.
  - Traefik and ingress controllers watch the Kubernetes API (EndpointSlices)
    instead. That is faster than DNS, but it needs a Kubernetes client and
    cluster permissions, and divisor "never calls the Kubernetes API"
    (docs/deployment/kubernetes.md:3-5). DNS is the right first step: it also
    works outside Kubernetes.
- **Verified in the code** (2026-11-16, fasthttp v1.78.0):
  - fasthttp's `TCPDialer` "dials all the resolved TCP addresses in
    round-robin manner until connection is established" and caches them for
    `DNSCacheDuration` (`tcpdialer.go`). divisor sets that from
    `server.dns_cache_duration`, one minute by default
    ([pkg/config/config.go:106](pkg/config/config.go#L106)).
  - The Probe client has its own dialer with the same cache duration
    ([pkg/config/config.go:349](pkg/config/config.go#L349)).
  - `ApplyReload` "relies on the Pool listing its Backends in the running
    config's order"
    ([core/balancer.go:84-85](core/balancer.go#L84-L85)). `PlanReload`
    reports kept, added and removed Backends by config position, the stats
    rows are in config order, and a Backend's Index is its config position at
    boot.
  - `Pool.ReplaceBackends` already Probes added Backends concurrently before
    they Join, lets removed ones Leave and closes them, and leaves the
    survivors untouched.
- **Shape:**
  - An opt-in per entry: its host is resolved to every A and AAAA record, and
    each address becomes a Backend on the entry's port, with the entry's
    weight and Probe settings.
  - The lookup has its own timer. It never runs inside a Probe round or under
    `transitionsMu`: a lookup can hang for seconds. Only applying the
    difference, through `Pool.ReplaceBackends`, takes the lock.
  - A failed or empty lookup keeps the last set. An address is removed only
    when a successful answer no longer contains it. This is safe because
    every address is Probed: a stale one goes Down.
  - Two levels: a config entry, and the Backends it resolved to. Reload, the
    stats order and the Index work on entries; a resolved address keeps its
    Index while it stays resolved.
  - A lookup that fails at startup starts the entry with no Backends (zero
    Alive Backends is a served state) and keeps trying.
- **Cut, and why:**
  - *SRV records.* A and AAAA with the entry's port cover Kubernetes and
    Compose. SRV is for dynamic ports (Consul): wait for a real request.
  - *"Re-resolved every Probe round".* Replaced by the lookup's own timer
    (Shape).
- **Order:** after F1a. DNS is slow to notice: a stopping pod can stay in the
  answer for some seconds (the DNS server's cache, then divisor's refresh
  interval). Probes and F1a's retry cover that gap.
- **Cost:** about a week, moderate risk. The resolver loop is small. The work
  is the one-to-one assumption between a config entry and a Backend, in
  `ApplyReload`, `PlanReload`'s positions, the stats order and the Index.
- **Decisions:**
  - Config shape: the opt-in's name, the refresh interval and whether it is a
    knob.
  - An empty successful answer (a Service scaled to zero) is kept like a
    failure by the rule above. Is that right, or does the entry empty after
    some time, like HAProxy's hold times?
  - Metric labels: pod addresses change on every rollout, so `address` series
    churn, and [F4](#f4)'s histogram per Backend makes that many short-lived
    series. Label by the entry too? Decide together with F4.
  - How the Access log and `/stats` name the entry and the resolved address.
  - ip-hash places resolved Backends by address (ADR 0008,
    [bugs.md B1](bugs.md#b1)). Replicas resolve at different moments, so they
    disagree for a few seconds while the set changes: accepted?
  - With [F3](#f3)'s knob for the old `Host` rewrite: the entry's hostname,
    not the resolved address?
  - A Reload that changes or removes a resolved entry: what happens to its
    Backends and their stats.
  - `dns_cache_duration` no longer matters for a resolved entry, whose
    Backends are addresses. Say so in the docs.
  - The config shape leaves room for named Pools ([F13](#f13)'s hedge).
- **Docs:** docs/deployment/kubernetes.md: a Deployment behind a headless
  Service becomes the usual way, the StatefulSet list the alternative.
  `examples/kubernetes` and the chart's default config change together
  (`TestHelmChartConfigMatchesTheKubernetesExample`).

<a id="f7"></a>
### F7. The Client IP behind a trusted proxy

Reviewed 2026-11-16: moved from Medium to High, to ship in the same release
as [F3](#f3), which brings the `trusted_proxies` list (moved there on
2026-11-13). Fallback rules and two docs warnings are added, CDN headers are
cut, and PROXY protocol gets its own entry ([F17](#f17)).

- **Gap:** deferred from 1.0 ([TODO.md](TODO.md#10-behavior-fixes-agreed-spec-tests-already-red),
  X-Forwarded-For semantics: "1.0 adds no trusted-proxy config"). The Client
  IP is always the TCP peer
  ([docs/configuration.md:156-161](docs/configuration.md#L156-L161)): behind
  a load balancer or CDN that speaks HTTP, it is that proxy.
- **Why it matters:**
  - ip-hash is unusable there. Every client hashes by the proxy's few
    addresses, so nearly all traffic lands on one or two Backends.
  - The Access log's `client_ip` shows the proxy on every line, and
    `$remote_addr` carries the proxy's address.
  - `X-Forwarded-For` to the Backend is already right: divisor appends to the
    chain.
  - In the cloud, most deployments sit behind such a proxy.
  - Timing: once F3 ships `trusted_proxies`, a user who sets it and still
    sees the proxy in `client_ip` will take it for a bug. What is left after
    F3 is about a day of work, so "Medium, later" made no sense.
- **Other proxies** (recalled on 2026-11-16, not re-read: confirm before an
  ADR cites them): nginx (`set_real_ip_from` with `real_ip_header` and
  `real_ip_recursive`), Caddy (`trusted_proxies`), Traefik
  (`forwardedHeaders.trustedIPs`), Envoy (`xff_num_trusted_hops`). The
  rightmost entry that is not trusted is the safe choice: the leftmost can be
  forged by the client.
- **Verified in the code** (2026-11-16): the Client IP is read as
  `ctx.RemoteIP()` in three places: ip-hash's `Pick`
  ([core/ip-hash/ip_hash.go:67](core/ip-hash/ip_hash.go#L67)),
  `ReverseProxyHandler`
  ([internal/proxy/proxy.go:114](internal/proxy/proxy.go#L114): the
  `X-Forwarded-For` append, `$remote_addr`, the Access log entry) and
  `NoAliveBackends`' Access log line
  ([internal/proxy/proxy.go:401](internal/proxy/proxy.go#L401)).
- **Shape:**
  - When the TCP peer is in `server.trusted_proxies`, the Client IP is the
    rightmost `X-Forwarded-For` entry that is not in the list. One function,
    used by the three places, so ip-hash, the Access log and `$remote_addr`
    all follow.
  - Fallbacks: with no `X-Forwarded-For`, or with every entry trusted, the
    Client IP is the TCP peer. A port on an entry (`1.2.3.4:5678`,
    `[2001:db8::1]:443`) is stripped. At an entry that does not parse, the
    walk stops and the Client IP is the last address before it (the nearest
    trusted hop, or the TCP peer).
  - What divisor appends to `X-Forwarded-For` stays the TCP peer: the chain
    says whom divisor really saw.
  - An empty list, the default, costs nothing on the request path.
- **Cut, and why:**
  - *CDN headers (`CF-Connecting-IP`, `True-Client-IP`).* CDNs send
    `X-Forwarded-For` too, and a choice of header is a second knob to
    explain. Reopen on a real request.
- **Cannot be fixed here:**
  - `max_conns_per_ip` counts TCP connections before any request is read, so
    behind a proxy it limits the proxy, not the clients.
  - A load balancer that works at the TCP level adds no header:
    [F17](#f17).
- **Decisions:**
  - Confirm the fallbacks.
  - A shorthand for the private ranges (Caddy's `private_ranges`), for
    clusters where the balancer's addresses change?
  - CONTEXT.md defines the Client IP as the TCP peer and says divisor never
    reads a client-sent `X-Forwarded-For` to decide it. The definition
    changes with this feature: an ADR.
- **Docs:** docs/configuration.md:156-161, docs/observability.md
  (`client_ip`), examples/README.md (the ip-hash caveat), CONTEXT.md. Two
  warnings for [FD2](#fd2): a list that is too wide (`0.0.0.0/0`) lets any
  client choose its `client_ip` and its ip-hash Backend, and
  `max_conns_per_ip` behind a proxy limits the proxy.

<a id="f10"></a>
### F10. WebSockets

Reviewed 2026-11-16: moved from Medium to High, as its last and largest item.
"Connection upgrades" in general and WebSocket over HTTP/2 are cut, and
ending open streams at shutdown becomes a prerequisite.

- **Gap:** unsupported ([README.md:23-26](README.md#L23-L26)). `Upgrade` and
  `Connection` are stripped as hop-by-hop headers
  ([internal/proxy/proxy.go:44-53](internal/proxy/proxy.go#L44-L53)), so the
  Backend sees a plain GET and the WebSocket handshake fails.
- **Why it matters:**
  - There is no workaround inside divisor. An app with even one WebSocket
    endpoint (notifications, live updates, a chat widget) needs a second
    proxy for that path, and then there is little reason to keep divisor.
  - Priority: by this file's rule it changes what divisor is good for more
    than [F2](#f2) or [F4](#f4) do. It is last in High because it is the most
    expensive item reviewed: one to two weeks, in the code that owns
    connection lifetimes.
- **Other proxies** (recalled on 2026-11-16, not re-read: confirm before an
  ADR cites them): HAProxy, Caddy, Traefik and AWS ALB pass WebSockets
  automatically. nginx needs `proxy_set_header` lines for `Upgrade` and
  `Connection`, Envoy needs `upgrade_configs`. No general HTTP load balancer
  lacks it. nginx's 60s `proxy_read_timeout` closing a silent WebSocket is
  the most common complaint about WebSockets behind nginx.
- **Verified in the code** (2026-11-16, fasthttp v1.78.0):
  - fasthttp's server can hand over the client connection (`Hijack`,
    `KeepHijackedConns` in `server.go`). Its client has no such call
    (`client.go` has no hijack), so divisor has to dial the Backend itself
    for an upgraded connection, outside `HostClient` and its `max_conn`
    count.
  - The HTTP/2 stack is net/http, which also answers HTTP/1.1 clients over
    TLS ([README.md:23-26](README.md#L23-L26)).
  - Nothing ends an open Streamed response at shutdown: one still open at
    `graceful_shutdown_timeout` makes divisor exit 1 (ADR 0007).
- **Shape:**
  - `websocket` only: a request whose `Upgrade` names `websocket`. Any other
    `Upgrade` stays stripped, as today.
  - The handshake is forwarded to the picked Backend on a connection divisor
    dials itself. Only when the Backend answers `101 Switching Protocols`
    does divisor take over the client connection and copy bytes both ways.
    Any other answer goes back as a normal response.
  - On both stacks: fasthttp's `Hijack`, and net/http's `Hijacker` for the
    HTTP/1.1 requests the HTTP/2 stack serves, which is where browsers send
    WebSockets.
  - An open WebSocket is a Pending request until either side closes, and
    counts against the Backend's `max_conn` by divisor's own count.
  - Its own idle timeout: no byte in either direction for that long closes
    both sides. `read_timeout` and `write_timeout` stop applying after the
    switch.
  - Graceful shutdown closes open WebSockets, so a restart never waits out
    the timeout.
- **Cut, and why:**
  - *Connection upgrades in general.* Passing any `Upgrade` through allows
    h2c smuggling: a client upgrades to cleartext HTTP/2, and its later
    requests bypass the proxy's rules. An allow-list of one.
  - *WebSocket over HTTP/2 (RFC 8441, extended CONNECT).* Browsers use it
    only when the server offers it, and fall back to HTTP/1.1 by themselves.
- **Prerequisite:** ending open streams when the drain begins
  ([TODO.md](TODO.md#streaming)). WebSockets stay open for hours: without it,
  every restart of divisor waits the whole `graceful_shutdown_timeout` and
  exits 1.
- **Decisions:**
  - An ADR.
  - What Middleware sees: `OnRequest` for the handshake (authentication on it
    is the common need), `OnResponse` for the `101`? Nothing per frame.
  - The idle timeout's name and default: `stream_idle_timeout`'s 5m, or its
    own? Many apps keep a WebSocket silent for minutes.
  - How shutdown closes them: at once when the drain begins, or after a
    grace? A WebSocket Close frame (1001), or just closing the connection?
  - The Access log line: at close, like a Streamed response, with the
    duration and the bytes each way?
  - least-response-time's Score: the handshake's answer time, as a stream's
    first byte.
  - [F1a](#f1a): a handshake whose connection fails is retried like any
    request.
  - [F4](#f4): a gauge of open WebSockets, and how the `101` is counted.
  - Limits: a cap on open WebSockets? What `max_conns_per_ip` and
    `concurrency` mean after the switch.
- **Docs:** README.md:23-26, the [FD1](#fd1) line, docs/configuration.md.

## Medium

<a id="f1b"></a>
### F1b. A failed attempt triggers an immediate Probe

Split from [F1](#f1) on 2026-11-12. It replaces "passive ejection": F1's cut
list has the reasons.

- **Gap:** after [F1a](#f1a), a Down Backend the Probe has not noticed yet
  costs no errors, but every request Picked for it pays a failed attempt
  first. For a refused connection that is nothing. For a black-holed Backend
  it is the 3s dial timeout, on its share of requests, until the next Probe
  round ends.
- **Shape:** a failed attempt asks the Pool to Probe that Backend now. The
  result goes through `updateLiveness` under `transitionsMu` like a round's,
  so the Probe stays the only judge of liveness and the Pool the Balancer's
  one writer. At most one triggered Probe per Backend at a time. A refused
  Backend is Down within milliseconds; a black-holed one after the dial
  timeout plus the Probe's own timeout (about 8s with the defaults, not 35s).
  No threshold and no time window to configure.
- **Precedent:** HAProxy's `on-error fail-check` (its default mode) and
  `fastinter`: a traffic error feeds or speeds up the health check instead of
  deciding liveness by itself.
- **Risk:** faster detection widens the exposure to a network blip on
  divisor's side. Today only a blip during a Probe round takes Backends Down,
  for one interval. With this, any blip that fails a request and the Probe
  right after it does. So a Backend that a triggered Probe took Down should
  be Probed again sooner than the next round (HAProxy's `fastinter` and
  `downinter`), unless [F1c](#f1c) already made the interval short.
- **Decisions:**
  - Is it still needed once F1a and F1c have shipped? What is left is a 3s
    delay on a black-holed Backend's share of requests for one short
    interval. Measure that before building this.
  - `transitionsMu` is held for a whole sequential round, so a triggered
    Probe would wait behind it: Concurrent Probe rounds
    ([TODO.md](TODO.md#kubernetes)) first, or a path that does not wait for
    the round.
  - The limit on triggered Probes: one in flight per Backend; a minimum gap
    between two?
  - Whether a Backend it took Down gets an early Probe.
  - A triggered Probe is not a Probe round: `ProbeRoundStarted`
    (least-response-time's clock) must not fire for it.

<a id="f8"></a>
### F8. Affinity by header or cookie

Reviewed 2026-11-16 and split in two: the entry mixed two mechanisms with
very different costs. [F8a](#f8a) (hash on a header, Medium) follows.
[F8b](#f8b) (a sticky cookie, Low) waits for a real request. This section
holds what they share.

- **Gap, as the old entry stated it:** ip-hash keys on the Client IP, so
  every client behind one NAT lands on one Backend, and a client changing
  networks moves.
- **What the review found:**
  - The two halves are different mechanisms. A header hash is ip-hash with
    another key: stateless, and the same answer on every replica. A cookie
    that divisor sets is classic sticky sessions: state on the client, and a
    layer over any Balancer.
  - The old question, "a new Balancer, or ip-hash options?", fits only the
    header hash. The cookie is neither.
  - The NAT gap is not what the header hash fixes: it needs a header the
    client already sends. Only the cookie frees affinity from the Client IP,
    and it does so by replacing ip-hash for those users.
  - Sticky cookies are still a common checkbox, mostly for older apps that
    keep sessions in memory. Current practice avoids them: load becomes
    uneven, and adding Backends does not rebalance pinned clients.
- **Other proxies** (recalled on 2026-11-16, not re-read: confirm before an
  ADR cites them):

  | Proxy | Hash on a header | Sticky cookie set by the proxy |
  |---|---|---|
  | nginx | Yes (`hash $key consistent`) | NGINX Plus only (`sticky cookie`) |
  | HAProxy | Yes (`balance hdr(...)`) | Yes (`cookie ... insert`) |
  | Caddy | Yes (`lb_policy header`) | Yes (`lb_policy cookie`) |
  | Traefik | No | Yes (`sticky.cookie`) |
  | Envoy | Yes (hash policy) | Yes (hash policy with a generated cookie) |
  | AWS ALB | No | Yes |

<a id="f8a"></a>
### F8a. ip-hash on a request header

Split from [F8](#f8).

- **Gain:** requests that carry the same key (a tenant ID, an API key) reach
  the same Backend whatever their address, so one tenant's requests hit the
  same warm cache.
- **Shape:** an ip-hash option naming a request header. Its value is hashed
  in place of the Client IP, and a request without the header falls back to
  the Client IP. The ring is the same, so placement stays by config alone
  (ADR 0008) and replicas agree.
- **Cost:** small. `Pick` is one line that hashes the Client IP
  ([core/ip-hash/ip_hash.go:66-69](core/ip-hash/ip_hash.go#L66-L69)); a
  header value instead is a few lines and one config key.
- **Limit, to document:** the key has to exist before the first request.
  Hashing an app's own session cookie does not give sticky sessions: the
  first request has no cookie, the Backend that answers creates the session,
  and the new value hashes to another Backend. That case is [F8b](#f8b).
- **Order:** right after [F7](#f7), which already changes where ip-hash gets
  its key.
- **Decisions:**
  - Config shape: an option of `type: ip-hash`, or a Balancer type of its
    own?
  - A cookie's value as the key too? The same limit applies.
  - A header that is present but empty.
  - One hot key puts all its load on one Backend. Bounded-load hashing is
    not proposed: say so in the docs.
  - With [F1a](#f1a)'s retry: the ring's next owner, as for the Client IP.

<a id="f9"></a>
### F9. Helm chart values for real clusters

Reviewed 2026-11-16: kept at Medium, to do together with [F6](#f6)'s chart
change. It is mostly convenience. The review added the one item that is a
real gap (mounting a Secret) and a rule against a security trap in the old
shape.

- **Gap:** [charts/divisor/values.yaml](charts/divisor/values.yaml) exposes
  `replicaCount`, `image`, `terminationGracePeriodSeconds`, `resources` and
  `config` only. There is no way to set scheduling, annotations, the Service
  type, extra volumes or pull secrets.
- **Why it matters:**
  - Honestly, almost all of it is convenience. A Kubernetes user can patch
    any chart with kustomize, or edit `examples/kubernetes` by hand, and
    nothing on the list makes divisor do something new.
  - Still, a chart without these fails in the first hour on many clusters:
    tainted nodes need `tolerations`, a private registry mirror needs
    `imagePullSecrets`, exposing divisor needs the Service type and a cloud
    load balancer's annotations. Each is minutes of YAML.
  - One item is a capability: mounting a Secret (Verified).
- **Verified in the chart** (2026-11-16):
  - `resources` has shipped
    ([charts/divisor/values.yaml:14](charts/divisor/values.yaml#L14)).
  - The pod security settings are there already: non-root, read-only root
    filesystem, all capabilities dropped, no privilege escalation, seccomp
    `RuntimeDefault`
    ([charts/divisor/templates/deployment.yaml:20-60](charts/divisor/templates/deployment.yaml#L20-L60)).
    Not installed into a restricted namespace to confirm.
  - The only volume is the ConfigMap
    ([charts/divisor/templates/deployment.yaml:61-70](charts/divisor/templates/deployment.yaml#L61-L70)).
    `cert_file` and `key_file` need files, and HTTP/2 requires TLS, so TLS
    and HTTP/2 are impossible with the chart today. A Middleware given as a
    `file` has the same problem (one given as `code` fits in the config).
  - The Service is cluster-internal with the `http` port only
    ([charts/divisor/templates/service.yaml](charts/divisor/templates/service.yaml)).
    The pod has a `monitoring` port, bound to `0.0.0.0` by the chart's config
    for kubelet's probes.
- **Shape:**
  - Essentials: `extraVolumes` and `extraVolumeMounts`, `service.type` and
    `service.annotations`, `nodeSelector`, `tolerations`, `affinity`,
    `podAnnotations`, `podLabels`, `imagePullSecrets`.
  - Monitoring, second and opt-in: a PodMonitor (it needs no Service), or a
    separate Service for monitoring that is always cluster-internal, with a
    ServiceMonitor. An optional PodDisruptionBudget.
  - Rule: the monitoring port never goes on the main Service.
- **Changed from the old shape, and why:**
  - *"A monitoring port on the Service" is dropped.* The old shape proposed
    it next to `service.type`. The Monitoring server has no authentication:
    with the type set to `LoadBalancer`, the dashboard and `/metrics` would
    be on the internet.
  - *`extraVolumes` and `extraVolumeMounts` are added.* The only item that
    adds a capability: certificate Secrets and Middleware files.
  - *`resources` is done.*
- **Constraints:**
  - `examples/kubernetes` must change with it
    (`TestHelmChartConfigMatchesTheKubernetesExample`), and the release
    dry-run runs `helm lint` and kubeconform `-strict` on the rendered chart.
  - A PodMonitor or ServiceMonitor needs the Prometheus Operator's resource
    definitions: off by default, and kubeconform `-strict` fails on those
    kinds unless it is given their schemas.
  - Every value is an interface to keep working and to cover in the dry-run:
    few values and generic pass-throughs, not a long list.
- **Order:** with F6. The chart matters only once Kubernetes is a real
  target, and F6 changes the chart's default config and `examples/kubernetes`
  anyway.
- **Cost:** about a day for the essentials with the dry-run test, half a day
  more for the monitoring objects because of the schemas.
- **Decisions:**
  - A PodMonitor, a monitoring Service with a ServiceMonitor, or both.
  - `image.pullPolicy`: left out of the essentials, since the Kubernetes
    default suits the chart's release tags. Add it?
  - Does `examples/kubernetes` mirror the optional objects, or only the
    defaults?
  - A values example for TLS in the docs: a certificate Secret mounted
    through `extraVolumes` ([F2](#f2) makes its renewal work).

<a id="f16"></a>
### F16. Streaming on by default

Reviewed 2026-11-17: moved from Low to Medium, ordered after ending open
streams at shutdown and before [F10](#f10). The knob stays (decided). The
old entry's precondition is already met, and the large-response problem is
its own entry ([F19](#f19)).

- **Gap:** `server.streaming` is off by default (ADR 0007: "upgrading must
  not change the traffic of anyone who did not opt in", a 1.1 candidate).
  With the default config, a server-sent events endpoint answers 504 at
  `proxy_timeout`, and a response without a declared length is held whole in
  memory until it finishes.
- **Why it matters:** every other proxy streams by default (recalled, not
  re-read). A default divisor breaks an events endpoint in a way that is
  hard to diagnose.
- **Verified** (2026-11-17):
  - [bugs.md B4](bugs.md#b4) and [B5](bugs.md#b5) are fixed, so the two
    values of the knob answer failures the same way. The old entry's "fix
    B4 and B5 first" is met.
  - The streaming rule (`isStreamedResponse`,
    [internal/proxy/streaming.go:68-75](internal/proxy/streaming.go#L68-L75)):
    a response streams when it is `text/event-stream` or declares no
    `Content-Length`. Everything else is read whole, with the knob on too.
  - With the knob on, every response goes through `forwardAllowingStream`
    ([internal/proxy/streaming.go:25-56](internal/proxy/streaming.go#L25-L56)):
    a second response object, and for a response that does not stream, a
    move into the client's response. The Release benchmark runs the default
    config, so it does not measure that path today.
  - ADR 0007's Consequences, which become everyone's with the flip: a
    buffered response whose body breaks mid-read is not retried by fasthttp,
    even for an idempotent request; no Middleware sees a Streamed response's
    body; a non-zero `write_timeout` caps a stream's whole lifetime; a
    stream still open at `graceful_shutdown_timeout` makes divisor exit 1.
- **Decided (2026-11-17):** the knob stays. With streaming on, a Middleware
  that rewrites the body of a response without a declared length stops
  working, and `server.streaming: false` is the explicit "buffer everything"
  mode for it. ADR 0007 planned to delete the knob: amend it.
- **Shape:** `server.streaming` defaults to true. Nothing else changes.
- **Prerequisites:**
  - End open streams when the drain begins ([TODO.md](TODO.md#streaming)).
    Otherwise every user with a live events stream gets exit 1 on every
    restart. [F10](#f10) needs the same work, so it pays twice.
  - An A/B run of the Release benchmark with the knob on and off, before the
    flip: the knob puts every response on a longer path.
  - [F19](#f19), or at least its buffer sizing. Measured on 2026-11-17: one
    300 MB response with a declared length peaked at 1045 MB with the knob
    on, against 320 MB with it off. The flip alone would triple the memory
    of every large response.
- **Accepted with the flip, to state in the docs:**
  - The lost retry. [F1](#f1)'s cut list leans on fasthttp retrying on the
    same Backend: with streaming on, that covers failures before the
    response headers only.
  - A Middleware that rewrites bodies of responses without a declared length
    needs the knob off.
- **Decisions:**
  - The release: 1.1, as planned?
  - If the A/B run shows a cost on small responses: accept it, or shorten
    the path for a response that turns out not to stream?
  - `server.write_timeout` capping a whole stream
    ([TODO.md](TODO.md#streaming), optional item) matters more once
    streaming is the default: do it together?
- **Docs:** docs/configuration.md (the default), the README's Streamed
  responses section, docs/middleware.md (what `OnResponse` sees by default),
  the release notes, and an amendment to ADR 0007 (default on, knob kept).

<a id="f19"></a>
### F19. Large responses are held whole in memory

Added from the [F16](#f16) review and reviewed 2026-11-17, with a
measurement. It stays in Medium and ships together with F16's flip: with
streaming on, the problem is three times worse.

- **Gap:** a response that declares a `Content-Length` is read whole into
  memory before divisor answers, with either value of `server.streaming`:
  the streaming rule covers only event streams and responses without a
  declared length. No limit is set: the Backend client has no
  `MaxResponseBodySize` and the config has no key for one. A Backend that
  answers with a 5 GB file makes divisor hold 5 GB for that request.
  Request bodies have `max_request_body_size`; responses have nothing, and
  no doc says so.
- **Measured** (2026-11-17, on Windows, this checkout, one run each): one
  request for a 300 MB response with a declared length, through a fresh
  divisor, reading the process's peak working set before and after.

  | `server.streaming` | Peak before | Peak after | First byte | Whole request |
  |---|---|---|---|---|
  | `false` (the default) | 18 MB | 320 MB | 0.99s | 1.59s |
  | `true` | 18 MB | 1045 MB | 0.95s | 1.62s |

  - Off: memory equal to the body.
  - On: about 3.4 times the body. `moveWholeResponse` reads the stream into
    a buffer that starts empty and grows by doubling
    ([internal/proxy/streaming.go:84-94](internal/proxy/streaming.go#L84-L94)),
    so the outgrown buffers pile up beside the final one.
  - Either way the client got its first byte only after about one second of
    a 1.6 second request.
- **Why it matters:** downloads, exports and media through divisor cost
  memory equal to their size times their concurrency, and one large response
  can get the process killed. It is the only case in this file where
  ordinary traffic, one large download, can take down a default install.
- **Other proxies** (recalled on 2026-11-17, not re-read): none holds a whole
  response in memory before answering. nginx buffers, but in bounded memory
  with the rest on disk, and forwards while it reads.
- **Shape:**
  - Extend the streaming rule: a response also streams when its declared
    length is above a threshold. The rule is one function
    (`isStreamedResponse`,
    [internal/proxy/streaming.go:68-75](internal/proxy/streaming.go#L68-L75)),
    and everything after it is the existing stream machinery. The length is
    known, so the client still gets a `Content-Length` and no chunking.
  - Separately and at once: size `moveWholeResponse`'s buffer from the
    declared length, so the streaming-on path stops costing three times the
    body. A defect with a one-line fix.
- **Cut, and why:**
  - *A cap alone (fasthttp's `HostClient.MaxResponseBodySize`).* It protects
    memory by breaking large downloads instead of serving them.
- **Cost of the shape:** the same as for any Streamed response. No
  Middleware sees the body of a response above the threshold, and
  `server.streaming: false` is the way out (the knob stays, [F16](#f16)).
- **Order:** with F16, before the flip. Flipping the default alone would
  triple the memory of every large response.
- **Decisions:**
  - The threshold, and whether it is a knob.
  - With `server.streaming: false` nothing streams: a cap there, or nothing?
  - What the Access log and the Score record for such a response: as for a
    Streamed response?
- **Decided (2026-11-17):** the buffer sizing is tracked here, as part of
  this entry, not in [bugs.md](bugs.md).
- **Docs:** [FD1](#fd1) names the limitation until it is fixed, and the
  Streamed responses section of docs/configuration.md gains the threshold.

## Low (or large and later)

<a id="f8b"></a>
### F8b. Sticky sessions by a divisor-set cookie

Split from [F8](#f8) on 2026-11-16 and moved to Low: three to five days plus
the decisions, with no user asking. The design points are kept so a real
request can start from them.

- **Gap:** an app that keeps sessions in memory needs every request of a
  session on one Backend. ip-hash only approximates that by Client IP: every
  client behind one NAT lands on one Backend, and a client changing networks
  moves.
- **Shape:** not a Balancer and not an ip-hash option, but a layer in front
  of `Pick` that works with any Balancer, as in HAProxy and Traefik. The
  Balancer picks for a client without the cookie, and divisor sets the
  cookie on the response. Later requests go to the Backend the cookie names
  while it is Alive; otherwise the Balancer picks again and the cookie is
  replaced.
- **Design points:**
  - The cookie's value must name the same Backend on every replica: derived
    from the Backend's address, as ADR 0008 does for the ring, never from
    the Index ([bugs.md B1](bugs.md#b1)). It should not reveal the address:
    a hash of it.
  - Config surface: the cookie's name, lifetime, `Secure`, `HttpOnly`,
    `SameSite` and path.
  - A client can choose its Backend by forging the cookie. HAProxy and
    Traefik accept that: confirm.
  - Interactions: [F1a](#f1a) (the pinned Backend fails to connect: retry
    elsewhere and pin again?), [F6](#f6) (pod addresses change on every
    rollout, so every rollout breaks the pins), [F11](#f11) (does a draining
    Backend keep its pinned clients?), [F12](#f12).
  - The response gains a `Set-Cookie` header, on both stacks, and before a
    Streamed response's body.
- **Decisions:** whether it is wanted at all: wait for a real request.

<a id="f11"></a>
### F11. Draining a Backend

Reviewed 2026-11-16: moved from Medium to Low and tied to [F8b](#f8b). Its
gain today is cosmetic, and the need behind it is met by a docs page
([FD4](#fd4)).

- **Gap, as the old entry stated it:** taking a Backend out of rotation for a
  deploy means removing its entry, which also drops its stats and Index.
- **What the review found:**
  - The complaint is about a stats row, not about traffic. A Reload that
    removes an entry makes the Backend Leave the rotation and closes only
    its idle connections (`Pool.removeBackend`,
    [core/pool/pool.go:238-244](core/pool/pool.go#L238-L244);
    `ProxyClient.Close`,
    [internal/proxy/proxy.go:542-545](internal/proxy/proxy.go#L542-L545)), so
    in-flight requests finish. Removal already drains gracefully:
    `drain: true` would add only "keep the stats row and the Index".
  - For a deploy even that is doubtful: the Backend comes back as a new
    version, and fresh stats fit it.
  - Planned maintenance has three ways without errors, none of which needs
    this feature:
    1. The Backend fails its own health check before it stops. The Probe
       marks it Down, in-flight requests finish, then it stops. This is the
       standard pattern (Kubernetes readiness, AWS ALB deregistration), and
       [F1c](#f1c) makes it take seconds, not 30.
    2. The Backend shuts down gracefully: it closes its listener and
       finishes its in-flight requests. After [F1a](#f1a) new requests retry
       on another Backend, with nothing to configure in divisor.
    3. Removing the entry and Reloading, which works today.
  - `drain: true` helps only a Backend that can do neither of the first two,
    and removal covers that one.
- **Other proxies** (recalled on 2026-11-16, not re-read): HAProxy (the
  `drain` state) and NGINX Plus (the `drain` parameter) have it for sticky
  sessions: clients already pinned keep their Backend, new clients go
  elsewhere. Without sticky sessions, drain is just "out of rotation", which
  open-source nginx's `down` flag gives. Kubernetes and AWS ALB drain
  through readiness and deregistration.
- **Cost, more than one boolean:**
  - A third dimension in the Pool's state: a Backend is Alive or Down, and
    also drained or not. A successful Probe must not make a drained Backend
    Rejoin.
  - A new kind of Reload change: `PlanReload` knows kept, added and removed
    Backends. A kept Backend with a changed field is new, and
    `ReplaceBackends` leaves survivors untouched.
- **Shape, if it is ever built:** a reloadable `backends[].drain: true`: the
  Backend Leaves the rotation (in-flight requests finish), keeps its stats
  and stays Probed. With F8b, its pinned clients keep it and new clients do
  not get it. No HTTP endpoint for it on the Monitoring server, which is
  unauthenticated.
- **Decisions:** whether it is wanted at all: together with F8b, or on a real
  request.

<a id="f12"></a>
### F12. Slow start

Reviewed 2026-11-17: moved from Medium to Low and narrowed to
least-connection. Low benefit, high cost; the need is met on the app side,
which [FD4](#fd4) documents.

- **Gap:** a Backend that just started is often slow: its caches are empty,
  its connection pools are not open, and on the JVM its code is not compiled
  yet. A Backend that Rejoins, or that a Reload adds, takes its full share at
  once.
- **How bad it is, per Balancer** (the old entry did not say):
  - round-robin, random, w-round-robin: the Backend gets its fair share at
    once. A share, not a flood.
  - least-response-time: handled already. [F5](#f5) scores an unmeasured
    Backend at the median.
  - least-connection: the real case. `Pick` takes the Backend with the
    fewest Pending requests
    ([core/least-connection/least_connection.go:20-38](core/least-connection/least_connection.go#L20-L38)).
    One that just Joined has none, so it takes every new request until its
    count reaches the others'. That is bounded at equal concurrency, which
    can still be too much for a cold Backend.
  - ip-hash: a ramp cannot exist without moving clients between Backends,
    which defeats ip-hash.
- **Other proxies** (recalled on 2026-11-16, not re-read): HAProxy
  (`slowstart`), Envoy (for round-robin and least-request) and AWS ALB have
  it. In nginx it is NGINX Plus only, and not with the hash methods. Caddy
  and Traefik do not have it. It is a feature of mature load balancers, not
  a basic expectation. Most setups solve warm-up on the app side: the
  Backend does not report healthy until it is warm. That works with divisor
  today, since a Backend Rejoins only after a successful Probe.
- **Cost**, hidden in the old shape's "each Balancer implements the ramp in
  its own way":
  - Five implementations, and none possible for ip-hash.
  - A new kind of state. Balancers do selection only and change only on Join
    and Leave; a ramp makes them change with time.
  - w-round-robin rebuilds every Backend's slots on each Join and Leave.
    Weights that change every second mean constant rebuilds.
  - A large test matrix, for something the app can do by itself.
- **Shape, if it is ever built:** least-connection first, where Joining
  really hurts: for a configurable time after a Join the Backend gets a
  growing share. Never ip-hash.
- **Decisions:** whether it is wanted at all: on a real request, with numbers
  from a cold Backend under least-connection.

<a id="f13"></a>
### F13. Routing by host or path to several Pools

Reviewed 2026-11-17: stays in Low, with a decision. One Pool per process is
divisor's design position until the High list is done, and routing by exact
hostname is recorded as the first slice if this is ever built.

- **Gap:** one process is one Pool; different hosts or paths need separate
  divisor instances.
- **What the review found:**
  - This is not one more feature. It is the line between two products: a
    load balancer for one service (one listener, one Pool: divisor today)
    and a front door for many (nginx, Caddy, Traefik, HAProxy, AWS ALB).
    Almost every HTTP load balancer routes; not routing is unusual at
    layer 7 (recalled, not re-read).
  - What the gap costs: most sites have more than one service behind one
    address on port 443, and divisor cannot be their only proxy. They need
    one divisor per service plus something in front that routes by host,
    which is another proxy.
  - By this file's rule it is the biggest item in the file. Low is right
    about its size, not about its weight.
- **Decided (2026-11-17):** one Pool per process is divisor's design
  position for now ("one Pool, done well"). The High list comes first: every
  item on it makes divisor a credible balancer for one service.
- **Why not now:**
  - Routing never comes alone. Once it exists, users ask for path prefix
    stripping and rewrites, redirects (HTTP to HTTPS), a certificate per
    hostname, and timeouts, headers and Middleware per route. Each is small;
    together they are "become nginx".
  - It is two to four weeks by itself: the config's shape, Reload's scope,
    the stats, the metrics, the dashboard, and a host and path match on the
    request path the Release benchmark measures.
- **What helps if it is built:** the Pool and its Balancer are already one
  unit (`core.LoadBalancer`), so several of them behind a router is a
  natural design.
- **Shape, if it is ever built:** first slice, routing by exact hostname to
  named Pools, each with its own Balancer and Probes, and a default Pool. It
  raises no question of prefix stripping or of which rule wins, and it
  covers the most common split. Paths and rewrites later, or never. It
  changes the config's shape and Reload's scope: spec, grilling and an ADR
  first.
- **Hedge, to apply now:** [F4](#f4)'s metric labels and [F6](#f6)'s config
  leave room for a `pool` dimension, so adding it later is not a rename.
- **Decisions:** revisit the position when the High list is done.
- **Docs:** README.md and [FD1](#fd1) state the position as a design choice,
  with the pattern for several services: one divisor per service behind
  whatever routes (on Kubernetes, an Ingress or a Gateway).

<a id="f15"></a>
### F15. TLS to Backends

Reviewed 2026-11-17: stays in Low and unscheduled, to build on a real
request. The entry is expanded so that a request can start from it.

- **Gap:** divisor always speaks plain HTTP/1.1 to Backends, and an
  `https://` Backend URL is a startup error (ADR 0004).
- **Who needs it:**
  - Teams whose compliance rules demand encryption in transit everywhere,
    the private network included. They cannot use divisor at all today.
  - Backends that only speak HTTPS: managed services, apps that force it.
  - Backends across a network that is not trusted.
- **Who does not:** most deployments. TLS ends at the load balancer and the
  Backends sit on a private network. On Kubernetes with a service mesh, the
  mesh encrypts between pods without divisor knowing.
- **Why Low:** a workaround exists, the one ADR 0004 names: a plaintext hop
  to a local sidecar that does the TLS. It is clumsy but real, so unlike
  [F10](#f10) this is not a dead end. And nobody is asking yet.
- **Other proxies** (recalled on 2026-11-17, not re-read): every general
  proxy has it: nginx (`proxy_ssl_*`), HAProxy (`ssl verify` on a server),
  Caddy, Traefik, Envoy, AWS ALB.
- **Verified in the code** (2026-11-17):
  - ADR 0004 rejected it for 1.0 as "a real feature (cert verification
    knobs, SNI, per-backend scheme state threaded through Probes, proxying,
    and stats)", and kept it additive.
  - `max_conn_duration` defaults to 10s
    ([pkg/config/config.go:75](pkg/config/config.go#L75)): every Backend
    connection is replaced every 10 seconds.
  - `preReq` sets the request's scheme to `http`
    ([internal/proxy/proxy.go:277](internal/proxy/proxy.go#L277)).
  - A Streamed response finds its Backend connection through the dialed
    connection's `LocalAddr` (`backendConnOf`,
    [internal/proxy/streaming.go:339-344](internal/proxy/streaming.go#L339-L344)).
    A TLS connection passes `LocalAddr` through to the connection under it,
    so this should keep working (read, not run).
- **Shape:**
  - `https://` Backend URLs with port 443 as their default, fasthttp's
    `HostClient.IsTLS` with a TLS config, and the Probe over TLS.
  - Verification on by default, against the system roots or a configured CA
    file.
  - A client certificate pair for Backends that ask for one, reloaded like
    [F2](#f2)'s.
- **Design points the old entry lacked:**
  - A knob to skip verification will be asked for, and it is dangerous. If
    it exists it is loud: a clear name and a warning at startup.
  - Which name is verified: after [F6](#f6) a Backend is an address, but a
    certificate names a host. The entry's hostname is the name checked and
    sent as SNI.
  - Connection lifetime: with the 10s `max_conn_duration`, TLS means a new
    handshake every 10 seconds per connection. A longer default for TLS
    Backends, or session resumption.
  - Are mixed Pools allowed, some Backends over TLS and some plain?
  - The integration suite needs Backends that speak TLS.
- **Cost:** three to five days, moderate risk.
- **Decisions:** whether to build it: on a real request. It is probably
  designed together with [F18](#f18), which needs the same Backend-side
  settings. An ADR superseding ADR 0004, with CONTEXT.md's TLS termination
  and Backend address entries.
- **Docs:** [FD1](#fd1) names "Backends are plaintext only" with the sidecar
  workaround; before this review only ADR 0004 said it.

<a id="f17"></a>
### F17. PROXY protocol on the listener

Added 2026-11-16 from the [F7](#f7) review, to make the limitation a recorded
decision. It is not scheduled.

- **Gap:** a load balancer that works at the TCP level cannot add
  `X-Forwarded-For`, and neither can any balancer in front of a divisor that
  terminates TLS itself. They pass the client's address with PROXY protocol
  (AWS NLB, HAProxy's `send-proxy`; recalled, not re-read). divisor does not
  read it, so in those setups the Client IP is the balancer and [F7](#f7)
  cannot help.
- **Shape:** an opt-in on the listener, accepted only from a peer in
  [F3](#f3)'s `trusted_proxies`. The address it carries becomes the TCP peer
  for everything after it, on both stacks.
- **Decisions:** whether it is wanted at all: wait for a real request.
  Version 1, version 2 or both. A library or divisor's own parser.

<a id="f18"></a>
### F18. gRPC, and HTTP/2 to Backends

Added 2026-11-16 from the [F10](#f10) review, to make the limitation a
recorded decision. It is not scheduled.

- **Gap:** gRPC needs HTTP/2 from end to end, with trailers and both
  directions streaming. divisor always speaks HTTP/1.1 to Backends
  ([README.md:23-26](README.md#L23-L26)) and buffers request bodies, so gRPC
  cannot pass through it. No doc says so.
- **Shape:** HTTP/2 to Backends (cleartext, or over TLS with [F15](#f15)) on
  a second Backend client, since fasthttp's `HostClient` is HTTP/1.1 only;
  request streaming ([TODO.md](TODO.md#streaming)); trailers in both
  directions.
- **Decisions:** whether it is wanted at all: wait for a real request. A
  second Backend client beside fasthttp's makes it large.
- **Docs:** until then the [FD1](#fd1) page names it.

## Not planned

Reviewed and declined. The entries stay so the reasoning is not lost, and so
the idea is not proposed again without something new.

<a id="f14"></a>
### F14. Per-Backend proxy_timeout

Reviewed 2026-11-17: declined. It has no use case. The real need is a
timeout per path, which belongs to routing ([F13](#f13)).

- **What it was:** a `backends[].proxy_timeout` override of
  `server.proxy_timeout`, deferred from 1.0 as additive
  ([TODO.md](TODO.md#config-surface-additions); ADR 0003, Considered
  Options).
- **Why it is declined:**
  - A divisor process is one Pool for one service ([F13](#f13), decided
    2026-11-17). Its Backends are copies of the same service, so they share
    one timeout.
  - No case for copies with different timeouts holds. A slower machine calls
    for a lower `weight`, not more patience. A Backend in a farther region
    adds milliseconds, and timeouts are in seconds. Two versions during a
    migration are temporary, and the larger timeout covers both.
  - What people need is a timeout per endpoint: seconds for an API call,
    minutes for a report or an upload. That is per path, so it is routing.
  - No other proxy has a response timeout per server (recalled on
    2026-11-17, not re-read): nginx sets it per location, HAProxy per
    backend group, Envoy per route or cluster, Traefik and Caddy per service
    or handler.
- **The counter-argument, considered:** it is nearly free.
  `Backend.ProxyTimeout` exists and is copied from the server setting
  ([pkg/config/config.go:146](pkg/config/config.go#L146),
  [:404](pkg/config/config.go#L404)), so it is one YAML tag, and `max_conn`
  and its relatives are per Backend already. But cheap is not a reason:
  every knob needs docs and tests and stays forever. "Additive" in ADR 0003
  meant it could be added without breaking anything, not that anyone wanted
  it.
- **Reopen if:** a user shows Backends in one Pool that need different
  timeouts.
- **Docs:** when ADR 0003 is next amended (for [F1a](#f1a)), its "deferred"
  line for this option becomes "declined".

## Docs

<a id="fd1"></a>
### FD1. A known-limitations page

Reviewed 2026-11-17: do it first. It has the best value per hour in this
file: about half a day, and it is the only item that helps users today.

- **Gap:** what divisor does not do is scattered or missing. Nowhere covers
  the 502 window before a Probe notices a Down Backend, the missing retry,
  certificate rotation, the ip-hash and least-response-time caveats, or the
  `Host` rewrite. The review of this file also found limits that no doc
  names at all (PROXY protocol, gRPC, large responses).
- **Shape:** one user-facing page in `docs/`, in two parts. Every limitation
  comes with what to do meanwhile.
  - **By design** (positions, not gaps):
    - One Pool per process ([F13](#f13)): one divisor per service, behind
      whatever routes.
    - Backends are plaintext only ([F15](#f15)): a local sidecar that does
      the TLS.
    - No TCP or UDP balancing and no HTTP/3
      ([README.md:23-26](README.md#L23-L26)).
  - **Not yet** (each line goes when its feature ships):
    - Probe-only failure detection and no retry ([F1](#f1)): a lower
      `health_checker_time`, and [FD4](#fd4)'s way of deploying.
    - Certificate rotation needs a restart ([F2](#f2)): restart from the
      renewal hook.
    - `Host` rewritten and no `X-Forwarded-Proto` ([F3](#f3)): a Middleware
      that sets the header.
    - No metrics by status code ([F4](#f4)): alert from the Access log.
    - Balancing per pod needs a StatefulSet ([F6](#f6)).
    - Behind a proxy the Client IP is that proxy, and `max_conns_per_ip`
      limits it ([F7](#f7), [F17](#f17)).
    - ip-hash behind NAT ([F8b](#f8b)).
    - No WebSockets ([F10](#f10)) and no gRPC ([F18](#f18)).
    - Event streams need `server.streaming: true` ([F16](#f16)).
    - Responses with a declared length are held whole in memory
      ([F19](#f19)), and uploads are buffered
      ([TODO.md](TODO.md#streaming)).
- **Changed from the old shape, and why:**
  - Two parts. A design position reads differently from a gap that will
    close, and the old list mixed them.
  - The list grew: the reviews added F4, F6, F7, F13, F15, F16, F17, F18
    and F19.
  - "What to do meanwhile" has content now: the workarounds the reviews
    found.
- **Keeping it true:** every reviewed entry's Docs bullet names its line
  here.
- **Docs:** move the ip-hash and least-response-time caveats here from
  examples/README.md ([bugs.md D1](bugs.md#d1)), and link the page from the
  README's "What divisor is".

<a id="fd2"></a>
### FD2. A hardening guide

Reviewed 2026-11-17: kept, after [FD1](#fd1), about half a day. It is
shorter than the old shape: a checklist by threat that links to the config
reference.

- **Gap:** production advice exists only implicitly, in
  `examples/production.config.yaml`.
- **Shape:** a short `docs/` page organized by threat. It links to
  docs/configuration.md for each key and does not restate it.
  - A public listener: slow clients, large bodies, connection floods.
    `read_header_timeout` (10s) and `idle_timeout` (120s) are bounded by
    default. `read_timeout` for bodies and `write_timeout` are unlimited on
    purpose, and the guide says when to set them. `max_request_body_size`.
    `max_conns_per_ip`, and that behind a proxy it limits the proxy
    ([F7](#f7)).
  - The Monitoring server: it has no authentication (the dashboard,
    `/stats`, `/metrics`), so keep it private. The most important line of
    the page.
  - Middleware files: Middleware code may import the whole standard library
    (`os/exec`, `net`, files), so a Middleware file deserves the same
    protection as the binary and the config.
  - TLS: `tls_min_version`.
  - Forged headers: today a client-sent `X-Forwarded-Proto` or
    `X-Forwarded-Host` reaches the Backend ([F3](#f3)). After F3 and
    [F7](#f7), a `trusted_proxies` list that is too wide lets a client
    choose its `client_ip` and its ip-hash Backend.
  - Memory: large responses ([F19](#f19)).
- **Changed from the old shape, and why:**
  - "Client timeouts (the defaults are unlimited)" was out of date.
    [bugs.md B11](bugs.md#b11) is fixed: `read_header_timeout` defaults to
    10s and `idle_timeout` to 120s
    ([pkg/config/config.go:93-94](pkg/config/config.go#L93-L94)).
  - Certificate rotation moved to [FD1](#fd1): it is a temporary limitation,
    not hardening.
  - A checklist by threat, not a tour of keys: a guide that restates the
    reference goes stale.
- **Keeping it true:** F3, F7 and [F9](#f9) (the rule for the monitoring
  port) add their lines when they ship.

<a id="fd3"></a>
### FD3. The docs site

Reviewed 2026-11-17: kept, as the last of the docs items. Its moment is just
before divisor is announced publicly.

- **What it is:** the mkdocs-material site, "the immediate post-1.0
  follow-up" ([TODO.md](TODO.md#docs)), planned before this review.
- **What it adds:** search, navigation and a more serious first impression.
  Every established proxy has a docs site, and evaluators notice.
- **What it costs:**
  - A build workflow, hosting and upkeep.
  - Links. The docs link out of `docs/` to `examples/` and `charts/`
    ([docs/deployment/kubernetes.md:43](docs/deployment/kubernetes.md#L43)
    is one; not counted). A site serves only what is inside `docs/`, so
    those links need rework.
- **Why last:** the Markdown already renders on GitHub, and there are no
  users yet. Content comes first ([FD1](#fd1), [FD4](#fd4), [FD2](#fd2)),
  and the High list now competes with it.
- **Decisions:** the hosting. Docs per release, or only the latest. How
  links to files outside `docs/` keep working both on GitHub and on the
  site.

<a id="fd4"></a>
### FD4. Deploying your Backends without errors

Added 2026-11-16 from the [F11](#f11) review, extended by the [F12](#f12)
review: what users need in place of a drain switch and of slow start.

- **Gap:** no doc says how to restart or deploy a Backend behind divisor
  without clients seeing 502s, or how to keep traffic off a Backend that is
  still warming up.
- **Shape:** a short section in `docs/`, writable today:
  - Stopping: make the Backend fail its health check first, wait for the
    Probe to mark it Down (up to `health_checker_time` +
    `health_checker_timeout`), let its in-flight requests finish, then stop
    it.
  - Or remove its entry and Reload, and add it back afterwards.
  - Starting: do not answer the health check with success until the Backend
    is warm. divisor sends it nothing before a Probe succeeds.
- **Update when features ship:** [F1c](#f1c) shortens the wait. After
  [F1a](#f1a), a Backend that closes its listener and finishes its in-flight
  requests needs nothing else.
