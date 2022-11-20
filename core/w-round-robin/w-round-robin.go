package w_round_robin

import (
	"sync/atomic"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/list/w_list"
	"github.com/valyala/fasthttp"
)

type WRoundRobin struct {
	servers []*http.HTTPClient
	len     uint64
	i       uint64
}

func NewWRoundRobin(config *config.Config) types.IBalancer {
	newWRoundRobin := &WRoundRobin{}

	tempServerList := w_list.NewWLinkedList()
	totalWeight := 0
	totalBackends := len(config.Backends)

	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		tempServerList.AddToTail(proxy, b.Weight)
		totalWeight += int(b.Weight)
	}
	tempServerList.Sort()

	startPoint := tempServerList.Head
	round := 1
	roundLimit := 0
	for i := 0; i < totalWeight; i++ {

		if startPoint.Weight >= uint(round) {
			newWRoundRobin.servers = append(newWRoundRobin.servers, startPoint.Proxy)
			if startPoint.Next != nil {
				startPoint = startPoint.Next
			} else {
				startPoint = tempServerList.Head
			}
		} else {
			startPoint = tempServerList.Head
			newWRoundRobin.servers = append(newWRoundRobin.servers, startPoint.Proxy)
		}

		roundLimit++
		if roundLimit == totalBackends {
			round++
			roundLimit = 0
		}
	}

	newWRoundRobin.len = uint64(len(newWRoundRobin.servers))
	return newWRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		w.next().ReverseProxyHandler(ctx)
	}
}

func (r *WRoundRobin) next() *http.HTTPClient {
	v := atomic.AddUint64(&r.i, 1)
	return r.servers[v%r.len]
}
