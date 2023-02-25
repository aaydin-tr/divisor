package w_round_robin

import (
	"math/rand"
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
	weight      uint
	isHostAlive bool
	i           int
}

type WRoundRobin struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           []proxy.IProxyClient
	len               uint64
	i                 uint64
	healthCheckerTime time.Duration
}

func NewWRoundRobin(config *config.Config, proxyFunc proxy.ProxyFunc) types.IBalancer {
	wRoundRobin := &WRoundRobin{
		isHostAlive:       config.HealthCheckerFunc,
		healthCheckerTime: config.HealthCheckerTime,
		serversMap:        make(map[uint32]*serverMap),
		hashFunc:          config.HashFunc,
	}

	for i, b := range config.Backends {
		if !wRoundRobin.isHostAlive(b.GetURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}

		proxy := proxyFunc(b, config.CustomHeaders)
		for i := 0; i < int(b.Weight); i++ {
			wRoundRobin.servers = append(wRoundRobin.servers, proxy)
		}

		wRoundRobin.serversMap[wRoundRobin.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxy, weight: b.Weight, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
	}

	if len(wRoundRobin.servers) <= 0 {
		return nil
	}
	wRoundRobin.len = uint64(len(wRoundRobin.servers))

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(wRoundRobin.servers), func(i, j int) {
		wRoundRobin.servers[i], wRoundRobin.servers[j] = wRoundRobin.servers[j], wRoundRobin.servers[i]
	})

	go wRoundRobin.healthChecker(config.Backends)

	return wRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		w.next().ReverseProxyHandler(ctx)
	}
}

func (w *WRoundRobin) next() proxy.IProxyClient {
	v := atomic.AddUint64(&w.i, 1)
	return w.servers[v%w.len]
}

func (w *WRoundRobin) healthChecker(backends []config.Backend) {
	for {
		select {
		case <-w.stopHealthChecker:
			return
		default:
			time.Sleep(w.healthCheckerTime)
			for i, backend := range backends {
				w.healthCheck(backend, i)
			}
		}
	}
}

func (w *WRoundRobin) healthCheck(backend config.Backend, index int) {
	status := w.isHostAlive(backend.GetURL())
	backendHash := w.hashFunc(helper.S2b(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := w.serversMap[backendHash]

	if ok && (!status && proxyMap.isHostAlive) {
		w.servers = helper.RemoveByValue(w.servers, proxyMap.proxy)

		w.len = w.len - uint64(proxyMap.weight)
		proxyMap.isHostAlive = false

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s Healt Check Status: %d ", backend.Url, status)
		if w.len == 0 {
			panic("All backends are down")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		for i := 0; i < int(proxyMap.weight); i++ {
			w.servers = append(w.servers, proxyMap.proxy)
		}

		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(w.servers), func(i, j int) {
			w.servers[i], w.servers[j] = w.servers[j], w.servers[i]
		})

		w.len = w.len + uint64(proxyMap.weight)
		proxyMap.isHostAlive = true
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s Healt Check Status: %d ", backend.Url, status)
	}
}

func (w *WRoundRobin) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(w.serversMap))
	for hash, p := range w.serversMap {
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
