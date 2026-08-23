package middleware

import (
	"errors"

	"github.com/valyala/fasthttp"
)

// ErrShortCircuit is returned by a Middleware that answered the request
// itself (see CONTEXT.md, Short-circuit): divisor sends ctx.Response as the
// Middleware left it, asks no Backend (OnRequest) or skips its own 502/504
// (OnResponse), and runs no later Middleware. Any other non-nil error means
// the Middleware failed and divisor answers 500. Wrapping is allowed; the
// match is errors.Is.
var ErrShortCircuit = errors.New("middleware short-circuit")

// Context is the request being proxied. It wraps fasthttp's RequestCtx,
// which divisor recycles for another request as soon as the handler returns:
// never keep ctx, its Request/Response, or any []byte read from them past
// the hook's return (copy what you need), and never touch it from a goroutine
// you started — a retained ctx reads or writes some other client's request.
type Context struct {
	*fasthttp.RequestCtx
}

func NewContext(ctx *fasthttp.RequestCtx) *Context {
	return &Context{ctx}
}

// Middleware is one config entry: New builds a single instance at startup and
// that instance serves every request concurrently, so any mutable field needs
// its own synchronization (sync.Mutex, atomics) — or keep per-request state
// on ctx, never on the struct.
//
// OnRequest hooks run in config order before the Backend is asked;
// OnResponse hooks run in reverse config order after the proxy attempt, err
// being the Backend failure if any, so the first middleware sees the response
// last. A short-circuit (ErrShortCircuit) or any other error from OnRequest
// skips every OnResponse; from OnResponse it skips the hooks that would run
// after it (the ones before it in config order).
type Middleware interface {
	OnRequest(ctx *Context) error
	OnResponse(ctx *Context, err error) error
}

type New func(config map[string]any) Middleware
