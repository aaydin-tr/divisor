package least_connection

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type leastConnection struct {
	pool.Rotation
	scanCursor atomic.Uint64
}

func New(*config.Config, []*pool.Backend) pool.Balancer {
	return &leastConnection{}
}

func (l *leastConnection) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := l.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	// The scan starts at a rotating offset so equally loaded Backends
	// (an idle pool) take turns instead of the lowest index winning every tie.
	offset := int(l.scanCursor.Add(1) % uint64(len(aliveBackends)))
	picked := aliveBackends[offset]
	leastPending := picked.Proxy.PendingRequests()
	for i := 1; i < len(aliveBackends); i++ {
		candidate := aliveBackends[(offset+i)%len(aliveBackends)]
		if pending := candidate.Proxy.PendingRequests(); pending < leastPending {
			picked = candidate
			leastPending = pending
		}
	}
	return picked
}
