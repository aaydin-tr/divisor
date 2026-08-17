package w_round_robin

import (
	"math/rand"
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
	weight      uint
	isHostAlive bool
	statsIdx    int
}

type WRoundRobin struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           atomic.Pointer[[]proxy.IProxyClient]
	i                 uint64
	healthCheckerTime time.Duration
}

func NewWRoundRobin(cfg *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	wRoundRobin := &WRoundRobin{
		isHostAlive:       cfg.HealthCheckerFunc,
		healthCheckerTime: cfg.HealthCheckerTime,
		serversMap:        make(map[uint32]*serverMap),
		hashFunc:          cfg.HashFunc,
		stopHealthChecker: make(chan bool),
	}

	servers := make([]proxy.IProxyClient, 0)
	for i, b := range cfg.Backends {
		proxyClient := proxyFunc(&b, cfg.CustomHeaders, middlewareExecutor)
		isHostAlive := wRoundRobin.isHostAlive(b.GetHealthCheckURL())
		if isHostAlive {
			for range int(b.Weight) {
				servers = append(servers, proxyClient)
			}
			zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		} else {
			zap.S().Warnf("Server is not live, it will be added for load balancing when its health check succeeds, Addr: %s", b.Url)
		}

		wRoundRobin.serversMap[wRoundRobin.hashFunc(helper.S2B(b.Url+strconv.Itoa(i)))] =
			&serverMap{proxy: proxyClient, weight: b.Weight, isHostAlive: isHostAlive, statsIdx: len(wRoundRobin.serversMap)}
	}

	if len(servers) == 0 {
		return nil
	}

	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(servers), func(i, j int) { //nolint:gosec
		servers[i], servers[j] = servers[j], servers[i]
	})
	wRoundRobin.servers.Store(&servers)

	go wRoundRobin.healthChecker(cfg.Backends)

	return wRoundRobin
}

func (w *WRoundRobin) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		w.next().ReverseProxyHandler(ctx) //nolint:errcheck
	}
}

func (w *WRoundRobin) next() proxy.IProxyClient {
	servers := *w.servers.Load()
	v := atomic.AddUint64(&w.i, 1)
	return servers[v%uint64(len(servers))]
}

func (w *WRoundRobin) healthChecker(backends []config.Backend) {
	for {
		select {
		case <-w.stopHealthChecker:
			return
		default:
			time.Sleep(w.healthCheckerTime)
			for i, backend := range backends {
				w.healthCheck(&backend, i)
			}
		}
	}
}

func (w *WRoundRobin) healthCheck(backend *config.Backend, index int) {
	status := w.isHostAlive(backend.GetHealthCheckURL())
	backendHash := w.hashFunc(helper.S2B(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := w.serversMap[backendHash]

	if ok && (!status && proxyMap.isHostAlive) {
		newServers := helper.RemoveByValue(*w.servers.Load(), proxyMap.proxy)
		w.servers.Store(&newServers)
		proxyMap.isHostAlive = false

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if len(newServers) == 0 {
			panic("All backends are down")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		oldServers := *w.servers.Load()
		newServers := make([]proxy.IProxyClient, 0, len(oldServers)+int(proxyMap.weight))
		newServers = append(newServers, oldServers...)
		for i := 0; i < int(proxyMap.weight); i++ {
			newServers = append(newServers, proxyMap.proxy)
		}

		rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(newServers), func(i, j int) { //nolint:gosec
			newServers[i], newServers[j] = newServers[j], newServers[i]
		})

		w.servers.Store(&newServers)
		proxyMap.isHostAlive = true
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
	}
}

func (w *WRoundRobin) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(w.serversMap))
	for hash, p := range w.serversMap {
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

func (w *WRoundRobin) Shutdown() error {
	zap.S().Info("Initiating graceful shutdown for Weighted Round Robin balancer")

	// Signal health checker to stop
	select {
	case w.stopHealthChecker <- true:
		zap.S().Debug("Health checker stop signal sent")
	default:
		zap.S().Debug("Health checker already stopped")
	}

	// Close all proxy connections
	for _, sm := range w.serversMap {
		if err := sm.proxy.Close(); err != nil {
			zap.S().Errorf("Error closing proxy connection: %s", err)
		}
	}

	zap.S().Info("Weighted Round Robin balancer shutdown completed")
	return nil
}
