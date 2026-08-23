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

// virtualNodesPerBackend is how many ring positions each Backend owns. The
// keyspace split between Backends approaches even as this grows and levels
// off around 100 (Ketama uses 160); a count that grew with the pool size
// gave one Backend a single position and two Backends four each.
const virtualNodesPerBackend = 100

// New places every Backend by its Index, so a ring Node's Id resolves to
// its Backend whatever order the slice came in.
func New(cfg *config.Config, backends []*pool.Backend) pool.Balancer {
	ringNodes := make([]*consistent.Node, len(backends))
	backendsByIndex := make([]*pool.Backend, len(backends))
	for _, b := range backends {
		ringNodes[b.Index] = &consistent.Node{Id: b.Index, Proxy: b.Proxy, Addr: b.Addr}
		backendsByIndex[b.Index] = b
	}
	return &ipHash{
		ring:      consistent.NewConsistentHash(virtualNodesPerBackend, cfg.HashFunc),
		hashFunc:  cfg.HashFunc,
		ringNodes: ringNodes,
		backends:  backendsByIndex,
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
