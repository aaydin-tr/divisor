package random

import (
	"math/rand/v2"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type random struct {
	pool.Rotation
}

func New(*config.Config) pool.Balancer {
	return &random{}
}

func (r *random) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := r.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	return aliveBackends[rand.IntN(len(aliveBackends))] //nolint:gosec
}
