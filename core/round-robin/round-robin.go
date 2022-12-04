package round_robin

import (
	"sync"
	"sync/atomic"
	"time"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type RoundRobin struct {
	servers    []*proxy.ProxyClient
	serversMap map[string]*proxy.ProxyClient
	len        uint64
	i          uint64

	healtCheckerFunc types.HealtCheckerType
	healtCheckerTime time.Duration
	mutex            sync.Mutex
}

func NewRoundRobin(config *config.Config, healtCheckerFunc types.HealtCheckerType, healtCheckerTime time.Duration) types.IBalancer {
	roundRobin := &RoundRobin{serversMap: make(map[string]*proxy.ProxyClient), healtCheckerFunc: healtCheckerFunc, healtCheckerTime: healtCheckerTime, mutex: sync.Mutex{}}

	for _, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)
		roundRobin.servers = append(roundRobin.servers, proxy)
		roundRobin.serversMap[b.Addr] = proxy
	}

	roundRobin.len = uint64(len(roundRobin.servers))
	if roundRobin.len <= 0 {
		return nil
	}

	go roundRobin.healtChecker(config.Backends)

	return roundRobin
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.next().ReverseProxyHandler(ctx)
	}
}

func (r *RoundRobin) next() *proxy.ProxyClient {
	v := atomic.AddUint64(&r.i, 1)
	return r.servers[v%r.len]
}

func (r *RoundRobin) healtChecker(backends []config.Backend) {
	for {
		time.Sleep(r.healtCheckerTime)
		//TODO Log
		for _, backend := range backends {
			go func(backend config.Backend) {
				r.mutex.Lock()
				defer r.mutex.Unlock()

				status := r.healtCheckerFunc(backend.GetURL())
				proxy, ok := r.serversMap[backend.Addr]
				if ok && status != 200 {
					index, err := helper.FindIndex(r.servers, proxy)
					if err != nil {
						//TODO log
						return
					}
					r.servers = helper.Remove(r.servers, index)
					r.len = r.len - 1
					delete(r.serversMap, backend.Addr)

					if r.len == 0 {
						panic("All backends are down")
					}
				} else if !ok && status == 200 {
					r.servers = append(r.servers, proxy)
					r.len++
					r.serversMap[backend.Addr] = proxy
				}
			}(backend)
		}
	}
}
