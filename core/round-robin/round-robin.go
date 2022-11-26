package round_robin

import (
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
	serversMap map[string]int
	len        uint64
	i          uint64

	healtCheckerFunc types.HealtCheckerType
	healtCheckerTime time.Duration
}

func NewRoundRobin(config *config.Config, healtCheckerFunc types.HealtCheckerType, healtCheckerTime time.Duration) types.IBalancer {
	roundRobin := &RoundRobin{serversMap: make(map[string]int), healtCheckerFunc: healtCheckerFunc, healtCheckerTime: healtCheckerTime}

	for _, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)
		roundRobin.servers = append(roundRobin.servers, proxy)
		roundRobin.serversMap[b.Addr] = len(roundRobin.servers) - 1
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
				status := r.healtCheckerFunc(backend.GetURL())
				index, ok := r.serversMap[backend.Addr]
				if ok && status != 200 {
					r.servers = helper.Remove(r.servers, index)
					r.len = r.len - 1
					delete(r.serversMap, backend.Addr)
				} else if !ok && status == 200 {
					proxy := proxy.NewProxyClient(backend)
					r.servers = append(r.servers, proxy)
					r.len++
					r.serversMap[backend.Addr] = len(r.servers) - 1
				}
			}(backend)
		}
	}
}
