# Upstream attempts are bounded by default; "unlimited" is inexpressible

`server.proxy_timeout` bounds each upstream attempt and defaults to **60s** — nginx's `proxy_read_timeout` default. Expiry surfaces as **504 Gateway Timeout**, and divisor never retries the request on another Backend. A zero or unset value means "use the default", not "unlimited": unlike `read_timeout`/`write_timeout`, where `0s` means unlimited, there is no way to configure an unbounded upstream wait.

The dial phase is bounded separately and more tightly: `internal/proxy` pins `fasthttp.Dial` as the dialer so connecting to a Backend fails within fasthttp's default 3s regardless of `proxy_timeout` (fasthttp would otherwise stretch the per-dial bound to the whole request timeout). A dial failure — refused, reset, or dial timeout to a black-holed address — means the Backend is *unreachable* and surfaces as **502 Bad Gateway** promptly; **504** specifically means "reachable but hanging past `proxy_timeout`". `TestUnreachableBackendGets502` pins the former, `TestPausedBackendBoundedFailure` the latter.

Before 1.0, `internal/proxy` called `Do` with no deadline, so one hanging Backend held every client request routed to it forever — `read_timeout`/`write_timeout` do not cover time spent inside the handler. 1.0 allows breaking changes, and this is one: configs that relied on the old unlimited wait get a 60s bound.

## Considered Options

- **Unlimited default, opt-in knob** — rejected: the out-of-box behavior would remain the exact hang this knob exists to kill, and nobody who hasn't already been bitten would set it.
- **`0` = unlimited, consistent with `read_timeout`** — rejected: YAML cannot distinguish "unset" from an explicit `0`, so the default could never be applied over a zero value without pointer-typed config; and making "hang forever" expressible again defeats the point. Someone who truly wants an effectively unbounded wait can set `proxy_timeout: 24h`.
- **Deriving the bound from `write_timeout`** — rejected: it muddles two unrelated semantics (response-write pacing vs upstream patience) and makes the bound invisible in the config.
- **Per-backend `backends[].proxy_timeout`** — deferred: a global knob covers the 1.0 spec; a per-backend override is additive and can ship later without breaking anything.

## Consequences

- A hanging Backend costs a client at most `proxy_timeout`; the Probe (5s timeout) evicts it independently.
- Clients can distinguish "Backend unreachable" (502) from "Backend hanging" (504).
- Long-polling upstreams slower than 60s need an explicit higher `proxy_timeout`; there is no off switch, only bigger numbers.
- Response streaming (SSE, never-ending bodies) is **not supported**: divisor reads the whole Backend response before answering, on both stacks, so nothing is flushed to the client until the Backend finishes — and a body that never finishes is a 504 at `proxy_timeout`. A bigger `proxy_timeout` does not make streaming work. This is a 1.0 non-goal, not a bug; streaming needs its own design (what `OnResponse` Middleware sees, what the bound means for an open stream) and is additive when it lands. (Amended 2026-08-23, review L8.)
- A client that disconnects does not cancel its Backend request, on either stack (`fasthttp.HostClient` has no cancellation hook; the net/http adapter does not consult `r.Context()`). The attempt runs until the Backend answers or `proxy_timeout` expires, then the result is discarded. `proxy_timeout` is therefore also the bound on work a vanished client can leave behind. (Amended 2026-08-23, review L6.)
- `TestPausedBackendBoundedFailure` pins the behavior end to end (bounded latency, 504, eviction, Rejoin).
