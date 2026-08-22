# A Middleware short-circuits by returning the sentinel `middleware.ErrShortCircuit`

A Middleware that answers the request itself (a Short-circuit, see CONTEXT.md) — denying it in `OnRequest` or replacing a Backend failure in `OnResponse` — writes the response to `ctx.Response` and returns `middleware.ErrShortCircuit` (matched with `errors.Is`, so wrapping is allowed). Divisor sends that response unchanged and runs no later Middleware. Any other non-nil error from either hook means the Middleware failed: divisor discards whatever is in the response and answers `500 {"message": …}`. Returning `nil` never stops the chain. Before this, the only way to keep a crafted response was to return an arbitrary error (logged and reported as a failure), `nil` let divisor overwrite a fallback with its own 502, and a response-shape heuristic decided which errors kept the response.

## Considered Options

- **A method on `Context`** (`ctx.ShortCircuit(); return nil`, the Gin/Echo `Abort()` shape) — rejected: it makes `return nil` sometimes stop the chain, so a reader must check a flag as well as the return value; the sentinel keeps the single rule "non-nil stops: the sentinel sends, anything else answers 500". It remains the natural home if `Context` grows a request-scoped API later; nothing here prevents adding it.
- **A response-modified heuristic** (`nil` + non-default response = send it) — rejected: fasthttp resets the response only before the attempt, so a Backend that fails mid-read leaves partial headers/body behind that are indistinguishable from a crafted fallback; and it cannot tell a Backend's 200 from a Middleware's 200, which is why a plain error after a successful Backend response used to be invisible to the client.

## Consequences

- Pre-1.0 behavior break: a Middleware that wrote a 403 and returned `errors.New("blocked")` now yields a 500 until it returns `middleware.ErrShortCircuit` instead.
- The 502/504 bodies divisor writes itself are generic (`bad gateway` / `gateway timeout`); the underlying error is logged, not sent.
