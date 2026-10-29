# Divisor

A layer-7 HTTP load balancer. Clients send requests to divisor; divisor picks a Backend and forwards the request to it.

## Language

### Load balancing

**Backend**:
One `backends` entry in the config: an upstream HTTP server that divisor forwards client requests to. Each entry is its own Backend with its own Probe, liveness, connection pool, and stats row — listing the same address twice yields two Backends that share a server (a legitimate way to double that server's share of traffic).
_Avoid_: server, upstream, target, node

**Balancer**:
The algorithm that picks which Backend serves a given request (round-robin, w-round-robin, ip-hash, random, least-connection, least-response-time).
_Avoid_: algorithm, strategy, scheduler

**Pool**:
All configured Backends together with each one's liveness. The Pool runs the Probes, moves Backends between Alive and Down, and tells the Balancer which Backends it may pick from; the Balancer never decides liveness itself.
_Avoid_: server list, server map, registry

**Index**:
A Backend's identity number for the life of the process: its config position at boot, and a number never used before for a Backend a Reload adds. It never changes when the list is reordered or shrinks, so after the first Reload that adds a Backend it no longer matches the config position. Shown as `backend_hash` in `/stats` and as the `index` label, next to `address`, on every per-Backend `/metrics` series, so two Backends sharing an address keep separate series; ip-hash places a Backend's Virtual nodes by it.
_Avoid_: backend hash, backend ID, position

**Backend address**:
The dialable `host:port` divisor connects to, derived from the config `url` key by validation: an optional `http://` scheme and a bare trailing slash are accepted and stripped, a missing port defaults to 80; a path, query, userinfo, or `https://` scheme is rejected at startup.
_Avoid_: backend URL, upstream address

**Probe**:
A single health-check request (GET on the Backend's `health_check_path`) sent to a Backend to decide whether it is Alive. A Probe succeeds when the Backend answers with a 2xx or 3xx status within the Probe timeout (`health_checker_timeout`, one deadline covering dial, write and read); a connection failure, a timeout, or any other status is a failed Probe. A Backend with a `health_check_status` overrides that rule: only that exact status is a successful Probe. Redirects are not followed.
_Avoid_: ping, heartbeat

**Alive / Down**:
The two liveness states of a Backend. Only Alive Backends receive traffic; a Down Backend keeps being probed so it can Rejoin.
_Avoid_: healthy/unhealthy, up/dead

**Rejoin**:
A Down Backend returning to the traffic rotation after a successful Probe.
_Avoid_: recovery, re-add

**Pending request**:
A request divisor has forwarded to a Backend and not yet received the response for; a Streamed response stays pending until its stream ends. It is what least-connection counts and compares — not TCP connections.
_Avoid_: in-flight request, active connection, open connection

**Client IP**:
The address of the TCP peer that opened the connection to divisor. It is what `X-Forwarded-For` is appended with, what `$remote_addr` carries, and what ip-hash hashes. Divisor never reads a client-sent `X-Forwarded-For` to decide it: behind another proxy, the Client IP is that proxy.
_Avoid_: real IP, remote address, origin IP

**Virtual node**:
One of the several positions a Backend occupies on the ip-hash ring so that client IPs spread evenly across Backends; a Backend leaves or rejoins the ring with all of its virtual nodes at once.
_Avoid_: replica, vnode

**TLS termination**:
Divisor decrypts client TLS itself; traffic from divisor to Backends is always plaintext HTTP. Divisor never speaks TLS to a Backend.
_Avoid_: end-to-end TLS, TLS passthrough (neither exists here)

**Middleware**:
A user-supplied Go snippet, declared in the config file, that runs before and/or after proxying and may mutate the request or response. Each config entry is constructed once at startup by its `New` function and that single instance serves every request concurrently; it must not keep the request context beyond the hook that received it.
_Avoid_: plugin, hook, interceptor

**Short-circuit**:
A Middleware answering the request itself: from `OnRequest`, so the Backend is never asked, or from `OnResponse`, replacing the Backend's answer (typically a failure, or a Streamed response before its first body byte) with a response of its own. Divisor sends that response unchanged, and no later Middleware runs: from `OnRequest`, the Middlewares after it (and no `OnResponse` at all); from `OnResponse`, the Middlewares before it in config order, since `OnResponse` runs in reverse order.
_Avoid_: handled, override, fallback, intercept, abort

**Request sequence number**:
The position of a request among all requests divisor has routed to one Backend since the process started: the first is 1, every later one is exactly one higher, and no two requests to the same Backend share a number. It equals the Backend's total request count at the moment the request was counted, so a request a Middleware short-circuits still consumes a number and the Backend sees a gap. Two Backends each have their own sequence; it restarts at 1 with the process. Exposed as the `$incremental` custom-header variable.
_Avoid_: incremental counter, request counter, request ID

**Access log**:
One JSON line per request divisor answers, written to stdout, describing who asked and how it was served: Client IP, method and path as the client sent them (the path raw, without its query: not percent-decoded or normalized; a Middleware rewrite is not logged), status, the Backend that served it (or that a Middleware short-circuited), duration, bytes out, and the Request sequence number. A Streamed response's line is written when its stream ends, so it covers the whole stream. Off by default. Application logs go to stderr; the access log is the only thing on stdout.
_Avoid_: request log, traffic log

**Application log**:
What divisor logs about itself — startup, Backends going Down and Rejoining, Reloads, shutdown — written to stderr only, JSON by default (`logging.format`, `logging.level`). Never mixed with the Access log, so each stream can be routed on its own.
_Avoid_: app log, error log, system log

**Monitoring server**:
The separate HTTP listener (`monitoring.host`/`monitoring.port`, `localhost:8001` by default) serving the stats dashboard, `/stats`, Prometheus `/metrics`, and the `/healthz` and `/ready` probe endpoints. Neither probe endpoint depends on Backend liveness.
_Avoid_: admin server, metrics server, stats server

**Streamed response**:
A Backend response divisor forwards to the client as bytes arrive instead of buffering it whole — what Server-Sent Events and large downloads need. Opt-in (`server.streaming`); once on, a response streams when it is an event stream (`text/event-stream`) or declares no length, and every other response is still buffered. `OnResponse` sees its status and headers, never its body, and can still Short-circuit before the first body byte; a client disconnect aborts the Backend stream.
_Avoid_: SSE mode, passthrough, chunked mode

**Reload**:
Applying an edited config file to a running divisor without restart: only the Backend set and Probe settings may change; an edit touching any other key — or an invalid new config — is rejected as a whole, divisor keeps serving with the old config and logs why. An entry left as it was keeps its Backend (liveness, stats) even if it moved in the list; a changed entry is a new Backend, so the old one is removed and the new one added. Triggered by SIGHUP or by a change to the config file, including a ConfigMap update (which replaces the file rather than writing it); a burst of changes is one Reload.
_Avoid_: hot reload, live update, refresh

### Integration testing

**Integration suite**:
The black-box test suite in which divisor and its Backends each run as Docker containers and tests drive real HTTP traffic from outside.
_Avoid_: e2e suite, docker tests

**Echo backend**:
The purpose-built test Backend image that identifies which instance served a request and echoes the request back (method, headers, body), with knobs for delay and forced failure, plus an endpoint that streams Server-Sent Events.
_Avoid_: mock server, stub, fake

**Scenario**:
One divisor configuration under test (Balancer type × HTTP version × TLS on/off). A Scenario owns one divisor container plus its Echo backends; tests grouped under a Scenario share them.
_Avoid_: fixture, environment, setup

**Failover**:
Traffic rerouting to the remaining Alive Backends after a Backend goes Down.
_Avoid_: fallback

**Spec-red**:
A test written against agreed 1.0 spec behavior that has not shipped yet; it skips unless explicitly opted into, so the blocking CI job stays green while the spec gap stays tracked. Shipping the behavior removes the marker and the test starts gating PRs.
_Avoid_: born-red, expected-failure, xfail
