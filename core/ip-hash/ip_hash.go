package ip_hash

import (
	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/consistent"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

// ipHash keeps one ring Node per Backend for its whole life; Join and Leave
// add or remove all of its virtual nodes at once.
type ipHash struct {
	ring      *consistent.ConsistentHash
	hashFunc  types.HashFunc
	ringNodes []*consistent.Node
	backends  []*pool.Backend
}

func New(cfg *config.Config, backends []*pool.Backend) pool.Balancer {
	nodes := make([]*consistent.Node, len(backends))
	for i, b := range backends {
		nodes[i] = &consistent.Node{Id: b.Index, Proxy: b.Proxy, Addr: b.Addr}
	}
	return &ipHash{
		ring:      consistent.NewConsistentHash(len(backends)*len(backends), cfg.HashFunc),
		hashFunc:  cfg.HashFunc,
		ringNodes: nodes,
		backends:  backends,
	}
}

func (h *ipHash) Join(b *pool.Backend) {
	h.ring.AddNode(h.ringNodes[b.Index])
}

func (h *ipHash) Leave(b *pool.Backend) {
	h.ring.RemoveNode(h.ringNodes[b.Index])
}

func (h *ipHash) Pick(ctx *fasthttp.RequestCtx) *pool.Backend {
	node := h.ring.GetNode(h.hashFunc(helper.S2B(ctx.RemoteIP().String())))
	if node == nil {
		return nil
	}
	return h.backends[node.Id]
}
