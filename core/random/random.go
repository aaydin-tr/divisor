package random

import (
	"math/rand"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type Random struct {
	servers []*proxy.ProxyClient
	len     int
}

func NewRandom(config *config.Config) types.IBalancer {
	newRandom := &Random{}

	for _, b := range config.Backends {
		proxy := proxy.NewProxyClient(b)
		newRandom.servers = append(newRandom.servers, proxy)
	}

	newRandom.len = len(newRandom.servers)
	return newRandom
}

func (r *Random) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.next().ReverseProxyHandler(ctx)
	}
}

func (r *Random) next() *proxy.ProxyClient {
	return r.servers[rand.Intn(r.len)]
}
