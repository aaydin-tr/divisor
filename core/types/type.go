package types

import "github.com/valyala/fasthttp"

type IBalancer interface {
	Serve() func(ctx *fasthttp.RequestCtx)
}
