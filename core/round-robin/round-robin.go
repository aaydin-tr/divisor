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
	server *circular_list.Node
	mutex  sync.Mutex
}

func NewRoundRobin(config *config.Config) types.IBalancer {
	serverList := circular_list.NewCircularLinkedList()

	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		serverList.AddToTail(proxy)
	}

	return &RoundRobin{mutex: sync.Mutex{}, server: serverList.Head}
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.mutex.Lock()
		defer r.mutex.Unlock()

		r.server.Proxy.ReverseProxyHandler(ctx)
		r.next()
	}
}

func (r *RoundRobin) next() {
	r.server = r.server.Next
}
