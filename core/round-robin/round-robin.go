package round_robin

import (
	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	circular_list "github.com/aaydin-tr/balancer/pkg/list"
	"github.com/valyala/fasthttp"
)

type RoundRobin struct {
	list circular_list.List
}

func NewRoundRobin(config *config.Config) types.IBalancer {
	newRoundRobin := &RoundRobin{}
	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		newRoundRobin.list.AddToTail(proxy)
	}
	return newRoundRobin
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	backend := r.list.Head
	return func(ctx *fasthttp.RequestCtx) {
		backend.Proxy.ReverseProxyHandler(ctx)
		backend = backend.Next
	}
}
