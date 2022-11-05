package w_round_robin

import (
	"sync"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/http"
	"github.com/aaydin-tr/balancer/pkg/config"
	circular_list "github.com/aaydin-tr/balancer/pkg/list"
	"github.com/aaydin-tr/balancer/pkg/list/w_list"
	"github.com/valyala/fasthttp"
)

type WRoundRobin struct {
	tempList      w_list.List
	sortedList    circular_list.List
	totalBackends uint
	round         uint

	mutex       sync.Mutex
	roundLimit  uint
	totalWeight uint
	totalReq    uint
}

func NewWRoundRobin(config *config.Config) types.IBalancer {
	newWRoundRobin := &WRoundRobin{
		mutex: sync.Mutex{},
		round: 1,
	}

	for _, b := range config.Backends {
		proxy := http.NewProxyClient(b)
		newWRoundRobin.tempList.AddToTail(proxy, b.Weight)
		newWRoundRobin.totalWeight = newWRoundRobin.totalWeight + b.Weight
		newWRoundRobin.totalBackends++
	}
	newWRoundRobin.tempList.Sort()
	return newWRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	startPoint := w.tempList.Head
	w.sortedList.AddToTail(startPoint.Proxy)
	sortedBackends := w.sortedList.Head

	return func(ctx *fasthttp.RequestCtx) {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		w.roundLimit++

		if w.totalReq == w.totalWeight {
			sortedBackends.Proxy.ReverseProxyHandler(ctx)
			sortedBackends = sortedBackends.Next
			return
		}

		if w.round <= startPoint.Weight {
			startPoint.Proxy.ReverseProxyHandler(ctx)

			if startPoint.Next != nil && w.round <= startPoint.Next.Weight {
				startPoint = startPoint.Next
				w.sortedList.AddToTail(startPoint.Proxy)
			} else {
				startPoint = w.tempList.Head
				w.sortedList.AddToTail(startPoint.Proxy)
			}
		} else {
			startPoint = w.tempList.Head
			startPoint.Proxy.ReverseProxyHandler(ctx)
		}

		if w.roundLimit == w.totalBackends {
			w.round++
			w.roundLimit = 0
		}
		w.totalReq++
	}
}
