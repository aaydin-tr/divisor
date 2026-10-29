package ip_hash

import (
	"sync"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/consistent"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

// ipHash places each Backend on the ring by its Index; Join and Leave add or
// remove all of its virtual nodes at once.
type ipHash struct {
	ring     *consistent.ConsistentHash
	hashFunc types.HashFunc
	// Index -> *pool.Backend, stored on Join and never deleted: a Pick may
	// resolve a node from a ring snapshot taken before that Backend Left.
	backendsByIndex sync.Map
}

// virtualNodesPerBackend is how many ring positions each Backend owns. The
// keyspace split between Backends approaches even as this grows and levels
// off around 100 (Ketama uses 160); a count that grew with the pool size
// gave one Backend a single position and two Backends four each.
const virtualNodesPerBackend = 100

// New needs no Backends up front: a Reload Joins Backends this Balancer was
// not constructed with.
func New(cfg *config.Config, _ []*pool.Backend) pool.Balancer {
	return &ipHash{
		ring:     consistent.NewConsistentHash(virtualNodesPerBackend, cfg.HashFunc),
		hashFunc: cfg.HashFunc,
	}
}

func (h *ipHash) Join(backend *pool.Backend) {
	h.backendsByIndex.Store(backend.Index, backend)
	h.ring.AddNode(ringNodeOf(backend))
}

func (h *ipHash) Leave(backend *pool.Backend) {
	h.ring.RemoveNode(ringNodeOf(backend))
}

// The ring derives a node's positions from its Id and Addr alone, so the node
// Leave builds removes the positions Join added.
func ringNodeOf(backend *pool.Backend) *consistent.Node {
	return &consistent.Node{Id: backend.Index, Proxy: backend.Proxy, Addr: backend.Addr}
}

func (h *ipHash) Pick(ctx *fasthttp.RequestCtx) *pool.Backend {
	node := h.ring.GetNode(h.hashFunc(helper.S2B(ctx.RemoteIP().String())))
	if node == nil {
		return nil
	}
	backend, _ := h.backendsByIndex.Load(node.Id)
	return backend.(*pool.Backend)
}
