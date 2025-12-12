package least_algorithm

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
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

type LeastAlgorithm struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           []proxy.IProxyClient
	len               int
	healthCheckerTime time.Duration
	lastIndex         *uint32
	nextFunc          func() proxy.IProxyClient
}

func NewLeastAlgorithm(cfg *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	leastAlgorithm := &LeastAlgorithm{
		serversMap:        make(map[uint32]*serverMap),
		isHostAlive:       cfg.HealthCheckerFunc,
		healthCheckerTime: cfg.HealthCheckerTime,
		hashFunc:          cfg.HashFunc,
		stopHealthChecker: make(chan bool),
		lastIndex:         new(uint32),
	}

	for i, b := range cfg.Backends {
		if !leastAlgorithm.isHostAlive(b.GetHealthCheckURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxyClient := proxyFunc(&b, cfg.CustomHeaders, middlewareExecutor)
		leastAlgorithm.servers = append(leastAlgorithm.servers, proxyClient)
		leastAlgorithm.serversMap[leastAlgorithm.hashFunc(helper.S2B(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxyClient, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		leastAlgorithm.len++
	}

	if leastAlgorithm.len <= 0 {
		return nil
	}

	switch cfg.Type {
	case "least-connection":
		leastAlgorithm.nextFunc = leastAlgorithm.leastConnectionNext
	case "least-response-time":
		leastAlgorithm.nextFunc = leastAlgorithm.leastResponseTimeNext
	default:
		zap.S().Error("Invalid balancer type for least algorithms")
		return nil
	}

	go leastAlgorithm.healthChecker(cfg.Backends)

	return leastAlgorithm
}

func (l *LeastAlgorithm) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		l.nextFunc().ReverseProxyHandler(ctx) //nolint:errcheck
	}
}

func (l *LeastAlgorithm) leastConnectionNext() proxy.IProxyClient {
	proxyClient := l.servers[atomic.LoadUint32(l.lastIndex)]
	for i, proxy := range l.servers {
		if proxy.PendingRequests() < proxyClient.PendingRequests() {
			proxyClient = proxy
			atomic.StoreUint32(l.lastIndex, uint32(i))
			break
		}
	}
	return proxyClient
}

func (l *LeastAlgorithm) leastResponseTimeNext() proxy.IProxyClient {
	proxyClient := l.servers[atomic.LoadUint32(l.lastIndex)]
	for i, proxy := range l.servers {
		if proxy.AvgResponseTime() < proxyClient.AvgResponseTime() {
			proxyClient = proxy
			atomic.StoreUint32(l.lastIndex, uint32(i))
		}
	}

	return proxyClient
}

func (l *LeastAlgorithm) healthChecker(backends []config.Backend) {
	for {
		select {
		case <-l.stopHealthChecker:
			return
		default:
			time.Sleep(l.healthCheckerTime)
			for i, backend := range backends {
				l.healthCheck(&backend, i)
			}
		}
	}
}

func (l *LeastAlgorithm) healthCheck(backend *config.Backend, index int) {
	status := l.isHostAlive(backend.GetHealthCheckURL())
	backendHash := l.hashFunc(helper.S2B(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := l.serversMap[backendHash]
	if ok && (!status && proxyMap.isHostAlive) {
		l.servers = helper.RemoveByValue(l.servers, proxyMap.proxy)
		l.len--
		proxyMap.isHostAlive = false

		if atomic.LoadUint32(l.lastIndex) >= uint32(l.len) {
			atomic.StoreUint32(l.lastIndex, 0)
		}

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if l.len == 0 {
			panic("All backends are down")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		l.servers = append(l.servers, proxyMap.proxy)
		l.len++
		proxyMap.isHostAlive = true
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
	}
}

func (l *LeastAlgorithm) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(l.serversMap))
	for hash, p := range l.serversMap {
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

func (l *LeastAlgorithm) Shutdown() error {
	zap.S().Info("Initiating graceful shutdown for Least Algorithm balancer")

	// Signal health checker to stop
	select {
	case l.stopHealthChecker <- true:
		zap.S().Debug("Health checker stop signal sent")
	default:
		zap.S().Debug("Health checker already stopped")
	}

	// Close all proxy connections
	for _, sm := range l.serversMap {
		if err := sm.proxy.Close(); err != nil {
			zap.S().Errorf("Error closing proxy connection: %s", err)
		}
	}

	zap.S().Info("Least Algorithm balancer shutdown completed")
	return nil
}
