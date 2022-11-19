package round_robin

import (
	"sync/atomic"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/valyala/fasthttp"
)

type RoundRobin struct {
	servers []*http.HTTPClient
	len     uint64
	i       uint64
}

func NewRoundRobin(config *config.Config) types.IBalancer {
	roundRobin := &RoundRobin{}
	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		roundRobin.servers = append(roundRobin.servers, proxy)
	}
	roundRobin.len = uint64(len(roundRobin.servers))

	return roundRobin
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.next().ReverseProxyHandler(ctx)
	}
}

func (r *RoundRobin) next() *http.HTTPClient {
	v := atomic.AddUint64(&r.i, 1)
	return r.servers[v%r.len]
}
