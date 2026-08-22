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

type Context struct {
	*fasthttp.RequestCtx
}

func NewContext(ctx *fasthttp.RequestCtx) *Context {
	return &Context{ctx}
}

type Middleware interface {
	OnRequest(ctx *Context) error
	OnResponse(ctx *Context, err error) error
}

type New func(config map[string]any) Middleware
