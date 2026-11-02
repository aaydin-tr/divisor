package ip_hash

import (
	"strconv"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/consistent"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

// ipHash owns one ring position set per Backend; Join and Leave add or remove
// all of its virtual nodes at once.
type ipHash struct {
	ring     *consistent.ConsistentHash[*pool.Backend]
	hashFunc types.HashFunc
}

var _ pool.RingBalancer = (*ipHash)(nil)

// virtualNodesPerBackend is how many ring positions each Backend owns. The
// keyspace split between Backends approaches even as this grows and levels
// off around 100 (Ketama uses 160); a count that grew with the pool size
// gave one Backend a single position and two Backends four each.
const virtualNodesPerBackend = 100

// New needs no Backends up front: a Reload Joins Backends this Balancer was
// not constructed with.
func New(cfg *config.Config) pool.Balancer {
	return &ipHash{
		ring:     consistent.NewConsistentHash[*pool.Backend](virtualNodesPerBackend, cfg.HashFunc),
		hashFunc: cfg.HashFunc,
	}
}

func (h *ipHash) Join(backend *pool.Backend) {
	h.ring.AddNode(ringKeyOf(backend), backend)
}

func (h *ipHash) Leave(backend *pool.Backend) {
	h.ring.RemoveNode(ringKeyOf(backend), backend)
}

// Every new position goes in before any old one goes out, so no Client IP
// visits another address while entries at one address trade places.
func (h *ipHash) MoveToNewAddressOccurrences(shifts []pool.AddressOccurrenceShift) {
	for _, shift := range shifts {
		h.ring.AddNode(ringKeyOf(shift.Backend), shift.Backend)
	}
	for _, shift := range shifts {
		h.ring.RemoveNode(ringKey(shift.Backend.Addr, shift.PreviousAddressOccurrence), shift.Backend)
	}
}

func ringKeyOf(backend *pool.Backend) string {
	return ringKey(backend.Addr, backend.AddressOccurrence)
}

// Placed by the config alone, never by Index (development/adr/0008-ip-hash-placement-by-config.md).
func ringKey(addr string, addressOccurrence int) string {
	return addr + "#" + strconv.Itoa(addressOccurrence)
}

func (h *ipHash) Pick(ctx *fasthttp.RequestCtx) *pool.Backend {
	backend, _ := h.ring.OwnerOf(h.hashFunc(helper.S2B(ctx.RemoteIP().String())))
	return backend
}
