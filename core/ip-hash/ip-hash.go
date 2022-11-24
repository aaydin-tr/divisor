package ip_hash

import (
	"sync"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/pkg/list/ring"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type IPHash struct {
	server *ring.Node
	mutex  sync.Mutex

	ipMap map[string]*proxy.ProxyClient
}

func NewIPHash(config *config.Config) types.IBalancer {
	serverList := ring.NewRingLinkedList()
	ipMap := make(map[string]*proxy.ProxyClient)

	for _, b := range config.Backends {
		proxy := proxy.NewProxyClient(b)
		serverList.AddToTail(proxy)
	}

	return &IPHash{server: serverList.Head, ipMap: ipMap, mutex: sync.Mutex{}}
}

func (h *IPHash) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		hashCode := helper.HashFunc(ctx.RemoteIP().String())
		proxy := h.get(hashCode)
		if proxy == nil {
			h.mutex.Lock()
			proxy = h.set(hashCode)
			h.mutex.Unlock()
		}

		proxy.ReverseProxyHandler(ctx)
	}
}

func (h *IPHash) get(hashCode string) *proxy.ProxyClient {
	if p, ok := h.ipMap[hashCode]; ok {
		return p
	}

	return nil
}

func (h *IPHash) set(hashCode string) *proxy.ProxyClient {
	currServer := h.server
	h.ipMap[hashCode] = currServer.Proxy
	h.server = currServer.Next
	return currServer.Proxy
}
