package least_connection

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type serverMap struct {
	proxy       proxy.IProxyClient
	isHostAlive bool
	i           int
}

type LeastConnection struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           []proxy.IProxyClient
	len               int
	healthCheckerTime time.Duration
	lastIndex         *uint32
}

func NewLeastConnection(config *config.Config, proxyFunc proxy.ProxyFunc) types.IBalancer {
	leastConnection := &LeastConnection{
		serversMap:        make(map[uint32]*serverMap),
		isHostAlive:       config.HealthCheckerFunc,
		healthCheckerTime: config.HealthCheckerTime,
		hashFunc:          config.HashFunc,
		stopHealthChecker: make(chan bool),
		lastIndex:         new(uint32),
	}

	for i, b := range config.Backends {
		if !leastConnection.isHostAlive(b.GetHealthCheckURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxy := proxyFunc(b, config.CustomHeaders)
		leastConnection.servers = append(leastConnection.servers, proxy)
		leastConnection.serversMap[leastConnection.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{proxy: proxy, isHostAlive: true, i: i}
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
		leastConnection.len++
	}

	if leastConnection.len <= 0 {
		return nil
	}

	go leastConnection.healthChecker(config.Backends)

	return leastConnection
}

func (r *LeastConnection) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		r.next().ReverseProxyHandler(ctx)
	}
}

func (r *LeastConnection) next() proxy.IProxyClient {
	min := r.servers[atomic.LoadUint32(r.lastIndex)]
	for i, proxy := range r.servers {
		if proxy.PendingRequests() < min.PendingRequests() {
			min = proxy
			atomic.StoreUint32(r.lastIndex, uint32(i))
			break
		}
	}
	return min
}

func (r *LeastConnection) healthChecker(backends []config.Backend) {
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

func (r *LeastConnection) healthCheck(backend config.Backend, index int) {
	status := r.isHostAlive(backend.GetHealthCheckURL())
	backendHash := r.hashFunc(helper.S2b(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := r.serversMap[backendHash]
	if ok && (!status && proxyMap.isHostAlive) {
		r.servers = helper.RemoveByValue(r.servers, proxyMap.proxy)
		r.len = r.len - 1
		proxyMap.isHostAlive = false

		if atomic.LoadUint32(r.lastIndex) >= uint32(r.len) {
			atomic.StoreUint32(r.lastIndex, 0)
		}

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

func (r *LeastConnection) Stats() []types.ProxyStat {
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
