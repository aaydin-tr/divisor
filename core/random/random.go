package random

import (
	"math/rand"
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

type Random struct {
	serversMap       map[string]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	servers          []*proxy.ProxyClient
	len              int
	healtCheckerTime time.Duration
}

func NewRandom(config *config.Config, healtCheckerFunc types.HealtCheckerFunc, healtCheckerTime time.Duration, hashFunc types.HashFunc) types.IBalancer {
	random := &Random{
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
		random.servers = append(random.servers, proxy)
		random.serversMap[b.Addr] = &serverMap{proxy: proxy, isHostAlive: true}
	}

	random.len = len(random.servers)
	if random.len <= 0 {
		return nil
	}

	go random.healtChecker(config.Backends)

	return random
}

func (r *Random) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.next().ReverseProxyHandler(ctx)
	}
}

func (r *Random) next() *proxy.ProxyClient {
	return r.servers[rand.Intn(r.len)]
}

func (r *Random) healtChecker(backends []config.Backend) {
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
