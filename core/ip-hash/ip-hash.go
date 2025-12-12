package ip_hash

import (
	"math"
	"strconv"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/consistent"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type serverMap struct {
	node        *consistent.Node
	isHostAlive bool
	i           int
}

type IPHash struct {
	serversMap        map[uint32]*serverMap
	isHostAlive       types.IsHostAlive
	hashFunc          types.HashFunc
	stopHealthChecker chan bool
	servers           consistent.ConsistentHash
	len               int
	healthCheckerTime time.Duration
}

func NewIPHash(config *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	ipHash := &IPHash{
		servers: *consistent.NewConsistentHash(
			int(math.Pow(float64(len(config.Backends)), float64(2))),
			config.HashFunc,
		),
		serversMap:        make(map[uint32]*serverMap),
		isHostAlive:       config.HealthCheckerFunc,
		healthCheckerTime: config.HealthCheckerTime,
		hashFunc:          config.HashFunc,
		stopHealthChecker: make(chan bool),
	}

	for i, b := range config.Backends {
		if !ipHash.isHostAlive(b.GetHealthCheckURL()) {
			zap.S().Warnf("Could not add for load balancing because the server is not live, Addr: %s", b.Url)
			continue
		}
		proxy := proxyFunc(b, config.CustomHeaders, middlewareExecutor)
		node := &consistent.Node{Id: i, Proxy: proxy, Addr: b.Url}
		ipHash.servers.AddNode(node)
		ipHash.serversMap[ipHash.hashFunc(helper.S2b(b.Url+strconv.Itoa(i)))] = &serverMap{node: node, isHostAlive: true, i: i}
		ipHash.len++
		zap.S().Infof("Server add for load balancing successfully Addr: %s", b.Url)
	}

	if ipHash.len <= 0 {
		return nil
	}

	go ipHash.healthChecker(config.Backends)

	return ipHash
}

func (h *IPHash) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		hashCode := h.hashFunc(helper.S2b(ctx.RemoteIP().String()))
		proxy := h.get(hashCode)
		proxy.ReverseProxyHandler(ctx)
	}
}

func (h *IPHash) get(hashCode uint32) proxy.IProxyClient {
	node := h.servers.GetNode(hashCode)
	return node.Proxy
}

func (h *IPHash) healthChecker(backends []config.Backend) {
	for {
		select {
		case <-h.stopHealthChecker:
			return
		default:
			time.Sleep(h.healthCheckerTime)
			for i, backend := range backends {
				h.healthCheck(backend, i)
			}
		}
	}
}

func (h *IPHash) healthCheck(backend config.Backend, index int) {
	status := h.isHostAlive(backend.GetHealthCheckURL())
	backendHash := h.hashFunc(helper.S2b(backend.Url + strconv.Itoa(index)))
	proxyMap, ok := h.serversMap[backendHash]

	if ok && (!status && proxyMap.isHostAlive) {
		h.servers.RemoveNode(proxyMap.node)
		proxyMap.isHostAlive = false
		h.len = h.len - 1

		zap.S().Infof("Server is down, removing from load balancer, Addr: %s", backend.Url)
		if h.len == 0 {
			panic("All backends are down")
		}
	} else if ok && (status && !proxyMap.isHostAlive) {
		h.servers.AddNode(proxyMap.node)
		proxyMap.isHostAlive = true
		h.len = h.len + 1
		zap.S().Infof("Server is live again, adding back to load balancer, Addr: %s", backend.Url)
	}
}

func (h *IPHash) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(h.serversMap))
	for hash, p := range h.serversMap {
		s := p.node.Proxy.Stat()
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

func (h *IPHash) Shutdown() error {
	zap.S().Info("Initiating graceful shutdown for IP Hash balancer")

	// Signal health checker to stop
	select {
	case h.stopHealthChecker <- true:
		zap.S().Debug("Health checker stop signal sent")
	default:
		zap.S().Debug("Health checker already stopped")
	}

	// Close all proxy connections
	for _, sm := range h.serversMap {
		if err := sm.node.Proxy.Close(); err != nil {
			zap.S().Errorf("Error closing proxy connection: %s", err)
		}
	}

	zap.S().Info("IP Hash balancer shutdown completed")
	return nil
}
