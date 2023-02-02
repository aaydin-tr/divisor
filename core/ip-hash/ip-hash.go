package ip_hash

import (
	"math"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/consistent"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type serverMap struct {
	node        *consistent.Node
	isHostAlive bool
}

type IPHash struct {
	serverMap        map[string]*serverMap
	healtCheckerFunc types.HealtCheckerFunc
	hashFunc         types.HashFunc
	servers          consistent.ConsistentHash
	len              int
	healtCheckerTime time.Duration
}

func NewIPHash(config *config.Config, healtCheckerFunc types.HealtCheckerFunc, healtCheckerTime time.Duration, hashFunc types.HashFunc) types.IBalancer {
	ipHash := &IPHash{
		servers: *consistent.NewConsistentHash(
			int(math.Pow(float64(len(config.Backends)), float64(2))),
			hashFunc,
		),
		serverMap:        make(map[string]*serverMap),
		healtCheckerFunc: healtCheckerFunc,
		healtCheckerTime: healtCheckerTime,
		hashFunc:         hashFunc,
	}

	for i, b := range config.Backends {
		if !helper.IsHostAlive(b.GetURL()) {
			//TODO Log
			continue
		}
		proxy := proxy.NewProxyClient(b)
		node := &consistent.Node{Id: i, Proxy: proxy}
		ipHash.servers.AddNode(node)
		ipHash.serverMap[proxy.Addr] = &serverMap{node: node, isHostAlive: true}
	}

	ipHash.len = len(config.Backends)
	if ipHash.len <= 0 {
		return nil
	}

	go ipHash.healtChecker(config.Backends)

	return ipHash
}

func (i *IPHash) Stats() []types.ProxyStat {
	return nil
}

func (h *IPHash) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		hashCode := h.hashFunc(helper.S2b(ctx.RemoteIP().String()))
		proxy := h.get(hashCode)
		proxy.ReverseProxyHandler(ctx)
	}
}

func (h *IPHash) get(hashCode uint32) *proxy.ProxyClient {
	node := h.servers.GetNode(hashCode)
	return node.Proxy
}

func (h *IPHash) healtChecker(backends []config.Backend) {
	for {
		time.Sleep(h.healtCheckerTime)
		//TODO Log
		for _, backend := range backends {
			status := h.healtCheckerFunc(backend.GetURL())
			proxyMap, ok := h.serverMap[backend.Addr]

			if ok && (status != 200 && proxyMap.isHostAlive) {
				h.servers.RemoveNode(proxyMap.node)
				proxyMap.isHostAlive = false
				h.len = h.len - 1

				if h.len == 0 {
					panic("All backends are down")
				}
			} else if ok && (status == 200 && !proxyMap.isHostAlive) {
				h.servers.AddNode(proxyMap.node)
				proxyMap.isHostAlive = true
				h.len = h.len + 1
			}
		}
	}
}
