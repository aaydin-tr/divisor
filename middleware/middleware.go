package middleware

import (
	"github.com/valyala/fasthttp"
)

type Context struct {
	*fasthttp.RequestCtx
}

func NewContext(ctx *fasthttp.RequestCtx) *Context {
	return &Context{ctx}
}

type Middleware interface {
	OnRequest(ctx *Context) error
	OnResponse(ctx *Context)
}

type New func(config map[string]any) Middleware
