package core

import (
	ip_hash "github.com/aaydin-tr/divisor/core/ip-hash"
	least_connection "github.com/aaydin-tr/divisor/core/least-connection"
	least_response_time "github.com/aaydin-tr/divisor/core/least-response-time"
	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/random"
	round_robin "github.com/aaydin-tr/divisor/core/round-robin"
	"github.com/aaydin-tr/divisor/core/types"
	w_round_robin "github.com/aaydin-tr/divisor/core/w-round-robin"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

var balancers = map[string]func(*config.Config, []*pool.Backend) pool.Balancer{
	"round-robin":         round_robin.New,
	"w-round-robin":       w_round_robin.New,
	"ip-hash":             ip_hash.New,
	"random":              random.New,
	"least-connection":    least_connection.New,
	"least-response-time": least_response_time.New,
}

// loadBalancer is the Pool plus a Balancer, as the rest of the process sees
// them.
type loadBalancer struct {
	pool     *pool.Pool
	balancer pool.Balancer
}

// NewBalancer wires the config into Backends, a Balancer and a Pool. It
// returns nil when the type is unknown or no Backend is Alive at startup.
func NewBalancer(cfg *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) types.IBalancer {
	newBalancer, ok := balancers[cfg.Type]
	if !ok {
		zap.S().Errorf("Unknown balancer type %q", cfg.Type)
		return nil
	}

	backends := make([]*pool.Backend, 0, len(cfg.Backends))
	for i := range cfg.Backends {
		b := &cfg.Backends[i]
		backends = append(backends, &pool.Backend{
			Index:    i,
			Addr:     b.Url,
			Weight:   b.Weight,
			ProbeURL: b.GetHealthCheckURL(),
			Proxy:    proxyFunc(b, cfg.CustomHeaders, middlewareExecutor),
		})
	}

	balancer := newBalancer(cfg, backends)
	p := pool.NewPool(backends, cfg.HealthCheckerFunc, cfg.HealthCheckerTime, balancer)
	if p.AliveBackendCount() == 0 {
		return nil
	}
	p.StartHealthChecker()

	return &loadBalancer{pool: p, balancer: balancer}
}

func (l *loadBalancer) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		backend := l.balancer.Pick(ctx)
		if backend == nil {
			proxy.NoAliveBackends(ctx)
			return
		}
		backend.Proxy.ReverseProxyHandler(ctx) //nolint:errcheck
	}
}

func (l *loadBalancer) Stats() []types.ProxyStat {
	return l.pool.Stats()
}

func (l *loadBalancer) Shutdown() error {
	return l.pool.Shutdown()
}
