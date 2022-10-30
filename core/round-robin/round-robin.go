package round_robin

import (
	"sync"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	circular_list "github.com/aaydin-tr/balancer/pkg/list"
	"github.com/valyala/fasthttp"
)

type RoundRobin struct {
	list  circular_list.List
	mutex sync.Mutex
}

func NewRoundRobin(config *config.Config) types.IBalancer {
	newRoundRobin := &RoundRobin{mutex: sync.Mutex{}}
	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		newRoundRobin.list.AddToTail(proxy)
	}
	return newRoundRobin
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	backend := r.list.Head
	return func(ctx *fasthttp.RequestCtx) {
		r.mutex.Lock()
		defer r.mutex.Unlock()
		backend.Proxy.ReverseProxyHandler(ctx)
		backend = backend.Next
	}
}
