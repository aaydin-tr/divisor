package least_algorithm

import (
	"strconv"
	"sync"
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
	statsIdx    int
}

type LeastAlgorithm struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan struct{}
	healthCheckerDone chan struct{}
	stopOnce          sync.Once
	servers           atomic.Pointer[[]proxy.IProxyClient]
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
		stopHealthChecker: make(chan struct{}),
		healthCheckerDone: make(chan struct{}),
		lastIndex:         new(uint32),
	}

	servers := make([]proxy.IProxyClient, 0, len(cfg.Backends))
	for i, b := range cfg.Backends {
		proxyClient := proxyFunc(&b, cfg.CustomHeaders, middlewareExecutor)
		isHostAlive := leastAlgorithm.isHostAlive(b.GetHealthCheckURL())
		if isHostAlive {
			servers = append(servers, proxyClient)
			zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		} else {
			zap.S().Warnf("Server is not live, it will be added for load balancing when its health check succeeds, Addr: %s", b.Url)
		}
		leastAlgorithm.serversMap[leastAlgorithm.hashFunc(helper.S2B(b.Url+strconv.Itoa(i)))] =
			&serverMap{proxy: proxyClient, isHostAlive: isHostAlive, statsIdx: len(leastAlgorithm.serversMap)}
	}

	if len(servers) == 0 {
		return nil
	}
	leastAlgorithm.servers.Store(&servers)

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
		proxyClient := l.nextFunc()
		if proxyClient == nil {
			proxy.NoAliveBackends(ctx)
			return
		}
		proxyClient.ReverseProxyHandler(ctx) //nolint:errcheck
	}
}

func (l *LeastAlgorithm) leastConnectionNext() proxy.IProxyClient {
	servers := *l.servers.Load()
	if len(servers) == 0 {
		return nil
	}
	lastIndex := atomic.LoadUint32(l.lastIndex)
	if lastIndex >= uint32(len(servers)) {
		lastIndex = 0
	}
	proxyClient := servers[lastIndex]
	for i, proxy := range servers {
		if proxy.PendingRequests() < proxyClient.PendingRequests() {
			proxyClient = proxy
			atomic.StoreUint32(l.lastIndex, uint32(i))
			break
		}
	}
	return proxyClient
}

func (l *LeastAlgorithm) leastResponseTimeNext() proxy.IProxyClient {
	servers := *l.servers.Load()
	if len(servers) == 0 {
		return nil
	}
	proxyClient := servers[0]
	leastResTime := proxyClient.RecentResponseTime()
	for _, server := range servers {
		resTime := server.RecentResponseTime()
		// 0 means the Backend is unmeasured — never answered, or just
		// Rejoined: it wins outright so it gets its first sample.
		if resTime == 0 {
			return server
		}

		if resTime < leastResTime {
			proxyClient = server
			leastResTime = resTime
		}
	}

	return proxyClient
}

func (l *LeastAlgorithm) healthChecker(backends []config.Backend) {
	defer close(l.healthCheckerDone)
	ticker := time.NewTicker(l.healthCheckerTime)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopHealthChecker:
			return
		case <-ticker.C:
			for i, backend := range backends {
				if helper.IsClosed(l.stopHealthChecker) {
					return
				}
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
		newServers := helper.RemoveByValue(*l.servers.Load(), proxyMap.proxy)
		l.servers.Store(&newServers)
		if atomic.LoadUint32(l.lastIndex) >= uint32(len(newServers)) {
			atomic.StoreUint32(l.lastIndex, 0)
		}
		proxyMap.isHostAlive = false

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if len(newServers) == 0 {
			zap.S().Warn("All backends are down, serving 503 until a backend rejoins")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		// A Rejoining Backend starts unmeasured: its score from before it
		// went Down — possibly a failure penalty — says nothing about it now.
		proxyMap.proxy.ResetRecentResponseTime()
		oldServers := *l.servers.Load()
		newServers := make([]proxy.IProxyClient, 0, len(oldServers)+1)
		newServers = append(newServers, oldServers...)
		newServers = append(newServers, proxyMap.proxy)
		l.servers.Store(&newServers)
		proxyMap.isHostAlive = true
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
	}
}

func (l *LeastAlgorithm) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(l.serversMap))
	for hash, p := range l.serversMap {
		s := p.proxy.Stat()
		stats[p.statsIdx] = types.ProxyStat{
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

	// Close rather than send: a send is dropped whenever the checker is not
	// parked in its select, which is most of the time.
	l.stopOnce.Do(func() { close(l.stopHealthChecker) })
	select {
	case <-l.healthCheckerDone:
	case <-time.After(types.HealthCheckerStopTimeout):
		zap.S().Warn("Health checker did not stop in time, continuing shutdown")
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
