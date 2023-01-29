package types

import "github.com/valyala/fasthttp"

type HealtCheckerFunc func(string) int

type HashFunc func([]byte) uint32

type IBalancer interface {
	Serve() func(ctx *fasthttp.RequestCtx)
}
