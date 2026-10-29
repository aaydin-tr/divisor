# Backends are plaintext HTTP only; `https://` backend URLs are rejected

Divisor's model is TLS termination at the edge: it decrypts client TLS itself and always speaks plain HTTP/1.1 to Backends. Before 1.0 the config layer silently stripped `https://` from a backend `url` like `http://`, which turned a TLS-only Backend into one that either never passed a Probe (plain-HTTP health check gets a redirect) or — worse — received traffic **unencrypted** on port 80. 1.0 makes the model enforceable: config validation rejects `https://` backend URLs at startup with an error naming the TLS-termination model, instead of downgrading silently.

## Considered Options

- **Implement TLS to Backends** (`HostClient.IsTLS`, https Probe URLs) — rejected for 1.0: it is a real feature (cert verification knobs, SNI, per-backend scheme state threaded through Probes, proxying, and stats), not a validation fix, and nothing in the 1.0 spec asks for it. Rejecting loudly keeps the door open: adding TLS support later is additive, while silently downgrading today actively harms anyone who trusted the scheme.
- **Keep stripping silently** — rejected: the scheme is the user stating a security expectation; discarding it is the one option that can turn a config typo into plaintext traffic carrying production data.

## Consequences

- A TLS-only upstream cannot sit behind divisor in 1.0; the workaround is a plaintext hop (sidecar, local reverse proxy) the operator sets up knowingly.
- The error is immediate and names the constraint, replacing M6's failure mode (Backend mysteriously Down, or silent plaintext).
- CONTEXT.md's **TLS termination** and **Backend address** entries describe the same boundary; reversing this decision means changing both plus this ADR.
