package w_round_robin

import (
	"math/rand/v2"
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// wRoundRobin holds Weight copies of each Alive Backend in a shuffled
// rotation, so shares follow weight without bursts to one Backend.
type wRoundRobin struct {
	pool.Rotation
	requestCounter atomic.Uint64
}

func New(*config.Config, []*pool.Backend) pool.Balancer {
	return &wRoundRobin{}
}

func (w *wRoundRobin) Join(b *pool.Backend) {
	current := w.AliveBackends()
	next := make([]*pool.Backend, 0, len(current)+int(b.Weight))
	next = append(next, current...)
	for range int(b.Weight) {
		next = append(next, b)
	}
	rand.Shuffle(len(next), func(i, j int) { next[i], next[j] = next[j], next[i] })
	w.SetAliveBackends(next)
}

func (w *wRoundRobin) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := w.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	return aliveBackends[w.requestCounter.Add(1)%uint64(len(aliveBackends))]
}
