# Divisor

A layer-7 HTTP load balancer. Clients send requests to divisor; divisor picks a Backend and forwards the request to it.

## Language

### Load balancing

**Backend**:
An upstream HTTP server that divisor forwards client requests to.
_Avoid_: server, upstream, target, node

**Balancer**:
The algorithm that picks which Backend serves a given request (round-robin, w-round-robin, ip-hash, random, least-connection, least-response-time).
_Avoid_: algorithm, strategy, scheduler

**Probe**:
A single health-check request sent to a Backend to decide whether it is Alive.
_Avoid_: ping, heartbeat

**Alive / Down**:
The two liveness states of a Backend. Only Alive Backends receive traffic; a Down Backend keeps being probed so it can Rejoin.
_Avoid_: healthy/unhealthy, up/dead

**Rejoin**:
A Down Backend returning to the traffic rotation after a successful Probe.
_Avoid_: recovery, re-add

**TLS termination**:
Divisor decrypts client TLS itself; traffic from divisor to Backends is always plaintext HTTP. Divisor never speaks TLS to a Backend.
_Avoid_: end-to-end TLS, TLS passthrough (neither exists here)

**Middleware**:
A user-supplied Go snippet, declared in the config file, that runs before and/or after proxying and may mutate the request or response.
_Avoid_: plugin, hook, interceptor

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
