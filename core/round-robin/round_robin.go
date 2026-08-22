package round_robin

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type roundRobin struct {
	pool.Rotation
	requestCounter atomic.Uint64
}

func New(*config.Config, []*pool.Backend) pool.Balancer {
	return &roundRobin{}
}

func (r *roundRobin) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := r.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	return aliveBackends[r.requestCounter.Add(1)%uint64(len(aliveBackends))]
}
