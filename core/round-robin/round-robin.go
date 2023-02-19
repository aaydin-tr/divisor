package round_robin

import (
	"strconv"
	"sync/atomic"
	"time"

	types "github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type serverMap struct {
	proxy       proxy.IProxyClient
	isHostAlive bool
	i           int
}

type RoundRobin struct {
	serversMap       map[uint32]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	servers          []proxy.IProxyClient
	len              uint64
	i                uint64
	healtCheckerTime time.Duration
	hashFunc         types.HashFunc
}

func NewRoundRobin(config *config.Config, proxyFunc proxy.ProxyFunc) types.IBalancer {
	roundRobin := &RoundRobin{
		serversMap:       make(map[uint32]*serverMap),
		healtCheckerFunc: config.HealtCheckerFunc,
		healtCheckerTime: config.HealtCheckerTime,
		hashFunc:         config.HashFunc,
	}

	for i, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxy := proxyFunc(b, config.CustomHeaders)
		roundRobin.servers = append(roundRobin.servers, proxy)
		roundRobin.serversMap[roundRobin.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxy, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
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

func (r *RoundRobin) next() proxy.IProxyClient {
	v := atomic.AddUint64(&r.i, 1)
	return r.servers[v%r.len]
}

func (r *RoundRobin) healtChecker(backends []config.Backend) {
	for {
		time.Sleep(r.healtCheckerTime)
		for i, backend := range backends {
			status := r.healtCheckerFunc(backend.GetURL())
			backendHash := r.hashFunc(helper.S2b(backend.Url + strconv.Itoa(i)))
			proxyMap, ok := r.serversMap[backendHash]
			if ok && (status != 200 && proxyMap.isHostAlive) {
				r.servers = helper.Remove(r.servers, proxyMap.i)
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
				proxyMap.i = int(r.len)
				zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s Healt Check Status: %d ", backend.Url, status)
			}
		}
	}
}

func (r *RoundRobin) Stats() []types.ProxyStat {
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
