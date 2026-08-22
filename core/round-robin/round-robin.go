package round_robin

import (
	"strconv"
	"sync"
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
	isHostAlive atomic.Bool
	statsIdx    int
}

type RoundRobin struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan struct{}
	healthCheckerDone chan struct{}
	stopOnce          sync.Once
	servers           atomic.Pointer[[]proxy.IProxyClient]
	i                 uint64
	healthCheckerTime time.Duration
}

func NewRoundRobin(cfg *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	roundRobin := &RoundRobin{
		serversMap:        make(map[uint32]*serverMap),
		isHostAlive:       cfg.HealthCheckerFunc,
		healthCheckerTime: cfg.HealthCheckerTime,
		hashFunc:          cfg.HashFunc,
		stopHealthChecker: make(chan struct{}),
		healthCheckerDone: make(chan struct{}),
	}

	servers := make([]proxy.IProxyClient, 0, len(cfg.Backends))
	for i, b := range cfg.Backends {
		proxyClient := proxyFunc(&b, cfg.CustomHeaders, middlewareExecutor)
		isHostAlive := roundRobin.isHostAlive(b.GetHealthCheckURL())
		if isHostAlive {
			servers = append(servers, proxyClient)
			zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		} else {
			zap.S().Warnf("Server is not live, it will be added for load balancing when its health check succeeds, Addr: %s", b.Url)
		}
		backendState := &serverMap{proxy: proxyClient, statsIdx: len(roundRobin.serversMap)}
		backendState.isHostAlive.Store(isHostAlive)
		roundRobin.serversMap[roundRobin.hashFunc(helper.S2B(b.Url+strconv.Itoa(i)))] = backendState
	}

	if len(servers) == 0 {
		return nil
	}
	roundRobin.servers.Store(&servers)

	go roundRobin.healthChecker(cfg.Backends)

	return roundRobin
}

func (r *RoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		proxyClient := r.next()
		if proxyClient == nil {
			proxy.NoAliveBackends(ctx)
			return
		}
		proxyClient.ReverseProxyHandler(ctx) //nolint:errcheck
	}
}

func (r *RoundRobin) next() proxy.IProxyClient {
	servers := *r.servers.Load()
	if len(servers) == 0 {
		return nil
	}
	v := atomic.AddUint64(&r.i, 1)
	return servers[v%uint64(len(servers))]
}

func (r *RoundRobin) healthChecker(backends []config.Backend) {
	defer close(r.healthCheckerDone)
	ticker := time.NewTicker(r.healthCheckerTime)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopHealthChecker:
			return
		case <-ticker.C:
			for i, backend := range backends {
				if helper.IsClosed(r.stopHealthChecker) {
					return
				}
				r.healthCheck(&backend, i)
			}
		}
	}
}

func (r *RoundRobin) healthCheck(backend *config.Backend, index int) {
	status := r.isHostAlive(backend.GetHealthCheckURL())
	backendHash := r.hashFunc(helper.S2B(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := r.serversMap[backendHash]
	if ok && (!status && proxyMap.isHostAlive.Load()) {
		newServers := helper.RemoveByValue(*r.servers.Load(), proxyMap.proxy)
		r.servers.Store(&newServers)
		proxyMap.isHostAlive.Store(false)

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if len(newServers) == 0 {
			zap.S().Warn("All backends are down, serving 503 until a backend rejoins")
		}
	} else if ok && (status && !proxyMap.isHostAlive.Load()) {
		oldServers := *r.servers.Load()
		newServers := make([]proxy.IProxyClient, 0, len(oldServers)+1)
		newServers = append(newServers, oldServers...)
		newServers = append(newServers, proxyMap.proxy)
		r.servers.Store(&newServers)
		proxyMap.isHostAlive.Store(true)
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
	}
}

func (r *RoundRobin) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(r.serversMap))
	for hash, p := range r.serversMap {
		s := p.proxy.Stat()
		stats[p.statsIdx] = types.ProxyStat{
			Addr:          s.Addr,
			TotalReqCount: s.TotalReqCount,
			AvgResTime:    s.AvgResTime,
			LastUseTime:   s.LastUseTime,
			ConnsCount:    s.ConnsCount,
			IsHostAlive:   p.isHostAlive.Load(),
			BackendHash:   hash,
		}
	}

	return stats
}

func (r *RoundRobin) Shutdown() error {
	zap.S().Info("Initiating graceful shutdown for Round Robin balancer")

	// Close rather than send: a send is dropped whenever the checker is not
	// parked in its select, which is most of the time.
	r.stopOnce.Do(func() { close(r.stopHealthChecker) })
	select {
	case <-r.healthCheckerDone:
	case <-time.After(types.HealthCheckerStopTimeout):
		zap.S().Warn("Health checker did not stop in time, continuing shutdown")
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
