package least_response_time

import (
	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type leastResponseTime struct {
	pool.Rotation
}

func New(*config.Config, []*pool.Backend) pool.Balancer {
	return &leastResponseTime{}
}

func (l *leastResponseTime) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := l.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	picked := aliveBackends[0]
	leastResTime := picked.Proxy.RecentResponseTime()
	for _, candidate := range aliveBackends {
		resTime := candidate.Proxy.RecentResponseTime()
		// 0 means the Backend is unmeasured — never answered, or just
		// Rejoined: it wins outright so it gets its first sample.
		if resTime == 0 {
			return candidate
		}
		if resTime < leastResTime {
			picked = candidate
			leastResTime = resTime
		}
	}
	return picked
}
