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

**Backend address**:
The dialable `host:port` divisor connects to, derived from the config `url` key by validation: an optional `http://` scheme and a bare trailing slash are accepted and stripped, a missing port defaults to 80; a path, query, userinfo, or `https://` scheme is rejected at startup.
_Avoid_: backend URL, upstream address

**Probe**:
A single health-check request sent to a Backend to decide whether it is Alive.
_Avoid_: ping, heartbeat

**Alive / Down**:
The two liveness states of a Backend. Only Alive Backends receive traffic; a Down Backend keeps being probed so it can Rejoin.
_Avoid_: healthy/unhealthy, up/dead

**Rejoin**:
A Down Backend returning to the traffic rotation after a successful Probe.
_Avoid_: recovery, re-add

**Pending request**:
A request divisor has forwarded to a Backend and not yet received the response for. It is what least-connection counts and compares — not TCP connections.
_Avoid_: in-flight request, active connection, open connection

**Virtual node**:
One of the several positions a Backend occupies on the ip-hash ring so that client IPs spread evenly across Backends; a Backend leaves or rejoins the ring with all of its virtual nodes at once.
_Avoid_: replica, vnode

**TLS termination**:
Divisor decrypts client TLS itself; traffic from divisor to Backends is always plaintext HTTP. Divisor never speaks TLS to a Backend.
_Avoid_: end-to-end TLS, TLS passthrough (neither exists here)

**Middleware**:
A user-supplied Go snippet, declared in the config file, that runs before and/or after proxying and may mutate the request or response.
_Avoid_: plugin, hook, interceptor

**Short-circuit**:
A Middleware answering the request itself: from `OnRequest`, so the Backend is never asked, or from `OnResponse`, replacing a Backend failure with a response of its own. Divisor sends that response unchanged, and no later Middleware runs.
_Avoid_: handled, override, fallback, intercept, abort

**Request sequence number**:
The position of a request among all requests divisor has routed to one Backend since the process started: the first is 1, every later one is exactly one higher, and no two requests to the same Backend share a number. It equals the Backend's total request count at the moment the request was counted, so a request a Middleware short-circuits still consumes a number and the Backend sees a gap. Two Backends each have their own sequence; it restarts at 1 with the process. Exposed as the `$incremental` custom-header variable.
_Avoid_: incremental counter, request counter, request ID

### Integration testing

**Integration suite**:
The black-box test suite in which divisor and its Backends each run as Docker containers and tests drive real HTTP traffic from outside.
_Avoid_: e2e suite, docker tests

**Echo backend**:
The purpose-built test Backend image that identifies which instance served a request and echoes the request back (method, headers, body), with knobs for delay and forced failure.
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
