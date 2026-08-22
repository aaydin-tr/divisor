package pool

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/pkg/helper"
)

// Rotation is the Alive set as a copy-on-write slice, for Balancers that pick
// from a list: the Probe loop replaces it on Join/Leave, request goroutines
// only read it.
type Rotation struct {
	aliveBackends atomic.Pointer[[]*Backend]
}

func (r *Rotation) Join(b *Backend) {
	current := r.AliveBackends()
	next := make([]*Backend, 0, len(current)+1)
	next = append(next, current...)
	next = append(next, b)
	r.aliveBackends.Store(&next)
}

// Leave removes every copy of b, so a weighted rotation empties out too.
func (r *Rotation) Leave(b *Backend) {
	next := helper.RemoveByValue(r.AliveBackends(), b)
	r.SetAliveBackends(next)
}

// SetAliveBackends replaces the Alive slice; for Balancers that build their
// own order.
func (r *Rotation) SetAliveBackends(alive []*Backend) {
	r.aliveBackends.Store(&alive)
}

// AliveBackends returns the current Alive slice; callers must not mutate it.
func (r *Rotation) AliveBackends() []*Backend {
	if p := r.aliveBackends.Load(); p != nil {
		return *p
	}
	return nil
}
