package random

import (
	"math/rand"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/valyala/fasthttp"
)

type Random struct {
	servers []*http.HTTPClient
	len     int
}

func NewRandom(config *config.Config) types.IBalancer {
	newRandom := &Random{}

	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
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

func (r *Random) next() *http.HTTPClient {
	return r.servers[rand.Intn(r.len)]
}
