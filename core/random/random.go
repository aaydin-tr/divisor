package random

import (
	"math/rand"
	"strconv"
	"time"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type serverMap struct {
	proxy       *proxy.ProxyClient
	isHostAlive bool
	i           int
}

type Random struct {
	serversMap       map[uint32]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	servers          []*proxy.ProxyClient
	len              int
	healtCheckerTime time.Duration
	hashFunc         types.HashFunc
}

func NewRandom(config *config.Config, healtCheckerFunc types.HealtCheckerFunc, healtCheckerTime time.Duration, hashFunc types.HashFunc) types.IBalancer {
	random := &Random{
		serversMap:       make(map[uint32]*serverMap),
		healtCheckerFunc: healtCheckerFunc,
		healtCheckerTime: healtCheckerTime,
		hashFunc:         hashFunc,
	}

	for i, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxy := proxy.NewProxyClient(b, config.CustomHeaders)
		random.servers = append(random.servers, proxy)
		random.serversMap[random.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxy, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
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
		for i, backend := range backends {
			status := r.healtCheckerFunc(backend.GetURL())
			backendHash := r.hashFunc(helper.S2b(backend.Url + strconv.Itoa(i)))
			proxyMap, ok := r.serversMap[backendHash]
			if ok && (status != 200 && proxyMap.isHostAlive) {
				index, err := helper.FindIndex(r.servers, proxyMap.proxy)
				if err != nil {
					return
				}
				r.servers = helper.Remove(r.servers, index)
				r.len = r.len - 1
				proxyMap.isHostAlive = false

				zap.S().Infof("Server is down, removing from load balancer, Addr: %s Healt Check Status: %d ", backend.Url, status)
				if r.len == 0 {
					panic("All backends are down")
				}
			} else if ok && (status == 200 && !proxyMap.isHostAlive) {
				r.servers = append(r.servers, proxyMap.proxy)
				r.len++
				proxyMap.isHostAlive = true
				zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s Healt Check Status: %d ", backend.Url, status)
			}
		}
	}
}

func (r *Random) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(r.serversMap))
	for hash, p := range r.serversMap {
		s := p.proxy.Stat()
		stats[p.i] = types.ProxyStat{
			Addr:          s.Addr,
			TotalReqCount: s.TotalReqCount,
			AvgResTime:    s.AvgResTime,
			LastUseTime:   s.LastUseTime,
			ConnsCount:    s.ConnsCount,
			IsHostAlive:   p.isHostAlive,
			BackendHash:   hash,
		}
	}

	return stats
}
