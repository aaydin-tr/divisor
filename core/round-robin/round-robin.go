package round_robin

import (
	"strconv"
	"sync/atomic"
	"time"

	types "github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type serverMap struct {
	proxy       proxy.IProxyClient
	isHostAlive bool
	i           int
}

type RoundRobin struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           []proxy.IProxyClient
	len               uint64
	i                 uint64
	healthCheckerTime time.Duration
}

func NewRoundRobin(config *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	roundRobin := &RoundRobin{
		serversMap:        make(map[uint32]*serverMap),
		isHostAlive:       config.HealthCheckerFunc,
		healthCheckerTime: config.HealthCheckerTime,
		hashFunc:          config.HashFunc,
		stopHealthChecker: make(chan bool),
	}

	for i, b := range config.Backends {
		if !roundRobin.isHostAlive(b.GetHealthCheckURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxy := proxyFunc(b, config.CustomHeaders, middlewareExecutor)
		roundRobin.servers = append(roundRobin.servers, proxy)
		roundRobin.serversMap[roundRobin.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxy, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		roundRobin.len++
	}

	if roundRobin.len <= 0 {
		return nil
	}

	go roundRobin.healthChecker(config.Backends)

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

func (r *RoundRobin) healthChecker(backends []config.Backend) {
	for {
		select {
		case <-r.stopHealthChecker:
			return
		default:
			time.Sleep(r.healthCheckerTime)
			for i, backend := range backends {
				r.healthCheck(backend, i)
			}
		}
	}
}

func (r *RoundRobin) healthCheck(backend config.Backend, index int) {
	status := r.isHostAlive(backend.GetHealthCheckURL())
	backendHash := r.hashFunc(helper.S2b(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := r.serversMap[backendHash]
	if ok && (!status && proxyMap.isHostAlive) {
		r.servers = helper.RemoveByValue(r.servers, proxyMap.proxy)
		r.len = r.len - 1
		proxyMap.isHostAlive = false

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if r.len == 0 {
			panic("All backends are down")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		r.servers = append(r.servers, proxyMap.proxy)
		r.len++
		proxyMap.isHostAlive = true
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
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

func (r *RoundRobin) Shutdown() error {
	zap.S().Info("Initiating graceful shutdown for Round Robin balancer")

	// Signal health checker to stop
	select {
	case r.stopHealthChecker <- true:
		zap.S().Debug("Health checker stop signal sent")
	default:
		zap.S().Debug("Health checker already stopped")
	}

	// Close all proxy connections
	for _, sm := range r.serversMap {
		if err := sm.proxy.Close(); err != nil {
			zap.S().Errorf("Error closing proxy connection: %s", err)
		}
	}

	zap.S().Info("Round Robin balancer shutdown completed")
	return nil
}
