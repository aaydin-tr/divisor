package w_round_robin

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/pkg/list/w_list"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type serverMap struct {
	proxy   *proxy.ProxyClient
	indexes []int
	status  bool
}

type WRoundRobin struct {
	servers    []*proxy.ProxyClient
	serversMap map[string]*serverMap
	len        uint64
	i          uint64

	healtCheckerFunc types.HealtCheckerType
	healtCheckerTime time.Duration
	mutex            sync.Mutex
}

func NewWRoundRobin(config *config.Config, healtCheckerFunc types.HealtCheckerType, healtCheckerTime time.Duration) types.IBalancer {
	wRoundRobin := &WRoundRobin{healtCheckerFunc: healtCheckerFunc, healtCheckerTime: healtCheckerTime, mutex: sync.Mutex{}, serversMap: make(map[string]*serverMap)}

	tempServerList := w_list.NewWLinkedList()
	totalWeight := 0
	totalBackends := len(config.Backends)

	for _, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)
		tempServerList.AddToTail(proxy, b.Weight)
		totalWeight += int(b.Weight)
	}

	if tempServerList.Len <= 0 {
		return nil
	}

	tempServerList.Sort()

	startPoint := tempServerList.Head
	round := 1
	roundLimit := 0
	for i := 0; i < totalWeight; i++ {

		if startPoint.Weight >= uint(round) {
			wRoundRobin.servers = append(wRoundRobin.servers, startPoint.Proxy)
			if startPoint.Next != nil {
				startPoint = startPoint.Next
			} else {
				startPoint = tempServerList.Head
			}
		} else {
			startPoint = tempServerList.Head
			wRoundRobin.servers = append(wRoundRobin.servers, startPoint.Proxy)
		}

		roundLimit++
		if roundLimit == totalBackends {
			round++
			roundLimit = 0
		}
	}

	wRoundRobin.len = uint64(len(wRoundRobin.servers))
	for i, p := range wRoundRobin.servers {
		if sMap, ok := wRoundRobin.serversMap[p.Addr]; ok {
			sMap.status = true
			sMap.indexes = append(sMap.indexes, i)
		} else {
			wRoundRobin.serversMap[p.Addr] = &serverMap{status: true, indexes: []int{i}, proxy: p}
		}
	}

	go wRoundRobin.healtChecker(config.Backends)

	return wRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		w.next().ReverseProxyHandler(ctx)
	}
}

func (r *WRoundRobin) next() *proxy.ProxyClient {
	v := atomic.AddUint64(&r.i, 1)
	return r.servers[v%r.len]
}

func (r *WRoundRobin) healtChecker(backends []config.Backend) {
	for {
		time.Sleep(r.healtCheckerTime)
		//TODO Log
		for _, backend := range backends {
			status := r.healtCheckerFunc(backend.GetURL())
			serverMap := r.serversMap[backend.Addr]

			if serverMap.status && status != 200 {
				r.len = r.len - uint64(len(serverMap.indexes))

				sort.Slice(serverMap.indexes, func(i, j int) bool {
					return serverMap.indexes[i] > serverMap.indexes[j]
				})

				r.servers = helper.RemoveMultiple(r.servers, serverMap.indexes)
				serverMap.status = false

				if r.len == 0 {
					panic("All backends are down")
				}

			} else if !serverMap.status && status == 200 {
				r.len = r.len + uint64(len(serverMap.indexes))
				serverMap.status = true

				sort.Slice(serverMap.indexes, func(i, j int) bool {
					return serverMap.indexes[i] < serverMap.indexes[j]
				})

				for _, i := range serverMap.indexes {
					r.servers = helper.Insert(r.servers, i, serverMap.proxy)
				}
			}
		}
	}
}
