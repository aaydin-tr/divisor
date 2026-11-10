package w_round_robin

import (
	"math/rand/v2"
	"slices"
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// wRoundRobin shuffles its slots so a heavy Backend's share is spread out, not sent in one burst.
type wRoundRobin struct {
	// Copy-on-write: the Pool replaces it on Join/Leave, request goroutines only read it.
	slots atomic.Pointer[[]*pool.Backend]
	// One entry per Alive Backend; only the Pool (single writer) touches it.
	aliveBackends  []*pool.Backend
	requestCounter atomic.Uint64
}

func New(*config.Config) pool.Balancer {
	return &wRoundRobin{}
}

func (w *wRoundRobin) Join(b *pool.Backend) {
	w.aliveBackends = append(w.aliveBackends, b)
	w.rebuildSlots()
}

func (w *wRoundRobin) Leave(b *pool.Backend) {
	w.aliveBackends = slices.DeleteFunc(w.aliveBackends, func(alive *pool.Backend) bool { return alive == b })
	w.rebuildSlots()
}

// rebuildSlots recomputes every Alive Backend's slot count, since a Join
// or Leave can change the greatest common divisor of the Alive weights.
func (w *wRoundRobin) rebuildSlots() {
	weightGreatestCommonDivisor := greatestCommonDivisorOfWeights(w.aliveBackends)
	var slots []*pool.Backend
	for _, backend := range w.aliveBackends {
		for range backend.Weight / weightGreatestCommonDivisor {
			slots = append(slots, backend)
		}
	}
	rand.Shuffle(len(slots), func(i, j int) { slots[i], slots[j] = slots[j], slots[i] }) //nolint:gosec
	w.slots.Store(&slots)
}

func greatestCommonDivisorOfWeights(backends []*pool.Backend) uint {
	greatestCommonDivisorSoFar := uint(0)
	for _, backend := range backends {
		greatestCommonDivisorSoFar = greatestCommonDivisor(greatestCommonDivisorSoFar, backend.Weight)
	}
	return max(greatestCommonDivisorSoFar, 1)
}

func greatestCommonDivisor(a, b uint) uint {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (w *wRoundRobin) Pick(*fasthttp.RequestCtx) *pool.Backend {
	slots := w.slots.Load()
	if slots == nil || len(*slots) == 0 {
		return nil
	}
	return (*slots)[w.requestCounter.Add(1)%uint64(len(*slots))]
}
