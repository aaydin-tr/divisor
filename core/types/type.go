package types

import "github.com/valyala/fasthttp"

type HealtCheckerType func(string) int
type IBalancer interface {
	Serve() func(ctx *fasthttp.RequestCtx)
}
