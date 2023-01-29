package w_round_robin

import (
	"math/rand"
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
	weight      uint
	isHostAlive bool
}

type WRoundRobin struct {
	serversMap       map[string]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	servers          []*proxy.ProxyClient
	len              uint64
	i                uint64
	healtCheckerTime time.Duration
}

func NewWRoundRobin(config *config.Config, healtCheckerFunc types.HealtCheckerFunc, healtCheckerTime time.Duration, hashFunc types.HashFunc) types.IBalancer {
	wRoundRobin := &WRoundRobin{
		healtCheckerFunc: healtCheckerFunc,
		healtCheckerTime: healtCheckerTime,
		serversMap:       make(map[string]*serverMap),
	}

	for _, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)

		for i := 0; i < int(b.Weight); i++ {
			wRoundRobin.servers = append(wRoundRobin.servers, proxy)
		}
		wRoundRobin.serversMap[b.Addr] = &serverMap{proxy: proxy, weight: b.Weight, isHostAlive: true}

	}

	if len(wRoundRobin.servers) <= 0 {
		return nil
	}
	wRoundRobin.len = uint64(len(wRoundRobin.servers))

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(wRoundRobin.servers), func(i, j int) {
		wRoundRobin.servers[i], wRoundRobin.servers[j] = wRoundRobin.servers[j], wRoundRobin.servers[i]
	})

	go wRoundRobin.healtChecker(config.Backends)

	return wRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		w.next().ReverseProxyHandler(ctx)
	}
}

func (w *WRoundRobin) next() *proxy.ProxyClient {
	v := atomic.AddUint64(&w.i, 1)
	return w.servers[v%w.len]
}

func (w *WRoundRobin) healtChecker(backends []config.Backend) {
	for {
		time.Sleep(w.healtCheckerTime)
		//TODO Log
		for _, backend := range backends {
			status := w.healtCheckerFunc(backend.GetURL())
			proxyMap, ok := w.serversMap[backend.Addr]

			if ok && (status != 200 && proxyMap.isHostAlive) {
				w.servers = helper.RemoveMultipleByValue(w.servers, proxyMap.proxy)

				w.len = w.len - uint64(proxyMap.weight)
				proxyMap.isHostAlive = false

				if w.len == 0 {
					panic("All backends are down")
				}

			} else if ok && (status == 200 && !proxyMap.isHostAlive) {
				for i := 0; i < int(proxyMap.weight); i++ {
					w.servers = append(w.servers, proxyMap.proxy)
				}

				rand.Seed(time.Now().UnixNano())
				rand.Shuffle(len(w.servers), func(i, j int) {
					w.servers[i], w.servers[j] = w.servers[j], w.servers[i]
				})

				w.len = w.len + uint64(proxyMap.weight)
				proxyMap.isHostAlive = true
			}

		}
	}
}
