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

type serverMap struct {
	proxy       *proxy.ProxyClient
	isHostAlive bool
}

type RoundRobin struct {
	serversMap       map[string]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	servers          []*proxy.ProxyClient
	len              uint64
	i                uint64
	healtCheckerTime time.Duration
}

func NewRoundRobin(config *config.Config, healtCheckerFunc types.HealtCheckerFunc, healtCheckerTime time.Duration, hashFunc types.HashFunc) types.IBalancer {
	roundRobin := &RoundRobin{
		serversMap:       make(map[string]*serverMap),
		healtCheckerFunc: healtCheckerFunc,
		healtCheckerTime: healtCheckerTime,
	}

	for _, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)
		roundRobin.servers = append(roundRobin.servers, proxy)
		roundRobin.serversMap[b.Addr] = &serverMap{proxy: proxy, isHostAlive: true}
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
			status := r.healtCheckerFunc(backend.GetURL())
			proxyMap, ok := r.serversMap[backend.Addr]
			if ok && (status != 200 && proxyMap.isHostAlive) {
				index, err := helper.FindIndex(r.servers, proxyMap.proxy)
				if err != nil {
					//TODO log
					return
				}
				r.servers = helper.Remove(r.servers, index)
				r.len = r.len - 1
				proxyMap.isHostAlive = false

				if r.len == 0 {
					panic("All backends are down")
				}
			} else if ok && (status == 200 && !proxyMap.isHostAlive) {
				r.servers = append(r.servers, proxyMap.proxy)
				r.len++
				proxyMap.isHostAlive = true
			}
		}
	}
}
