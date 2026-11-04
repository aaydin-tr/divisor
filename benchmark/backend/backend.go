// Package backend is the Benchmark backend: every request, Probes included,
// gets the same fixed answer, so it is never what limits a Contender.
package backend

import (
	"bytes"
	"strconv"

	"github.com/valyala/fasthttp"
)

const (
	// AnsweredByHeader carries "<backend name>/<connection id>", the fasthttp
	// connection the request arrived on.
	AnsweredByHeader    = "X-Benchmark-Backend"
	ForwardedForHeader  = "X-Benchmark-Forwarded-For"
	ForwardedForPresent = "present"
	ForwardedForAbsent  = "absent"
)

const (
	fixedResponseBodySize = 1024
	decimalBase           = 10
)

var fixedResponseBody = bytes.Repeat([]byte{'x'}, fixedResponseBodySize)

func NewServer(backendName string) *fasthttp.Server {
	answeredByPrefix := backendName + "/"
	return &fasthttp.Server{
		Name: "benchmark-backend",
		Handler: func(ctx *fasthttp.RequestCtx) {
			answerWithTheFixedResponse(ctx, answeredByPrefix)
		},
	}
}

func answerWithTheFixedResponse(ctx *fasthttp.RequestCtx, answeredByPrefix string) {
	ctx.Response.Header.SetBytesV(AnsweredByHeader, strconv.AppendUint([]byte(answeredByPrefix), ctx.ConnID(), decimalBase))

	if len(ctx.Request.Header.Peek(fasthttp.HeaderXForwardedFor)) > 0 {
		ctx.Response.Header.Set(ForwardedForHeader, ForwardedForPresent)
	} else {
		ctx.Response.Header.Set(ForwardedForHeader, ForwardedForAbsent)
	}

	ctx.SetContentType("text/plain")
	ctx.SetBody(fixedResponseBody)
}
