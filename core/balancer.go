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
	config.RoundRobinType:         round_robin.New,
	config.WeightedRoundRobinType: w_round_robin.New,
	config.IPHashType:             ip_hash.New,
	config.RandomType:             random.New,
	config.LeastConnectionType:    least_connection.New,
	config.LeastResponseTimeType:  least_response_time.New,
}

// LoadBalancer is the Pool plus a Balancer, as the rest of the process sees
// them.
type LoadBalancer interface {
	types.IBalancer
	// ApplyReload swaps in an accepted Reload's Backend set and Probe
	// settings; calls must not overlap.
	ApplyReload(plan *config.ReloadPlan)
}

type loadBalancer struct {
	pool               *pool.Pool
	balancer           pool.Balancer
	newBackendProxy    proxy.ProxyFunc
	customHeaders      map[string]string
	middlewareExecutor *middleware.Executor
}

// NewBalancer wires the config into Backends, a Balancer and a Pool. It
// returns nil only when the type is unknown; zero Alive Backends at startup
// is a served state (503 until a Probe lets one Rejoin), so orchestrated
// deploys can start divisor before its Backends.
func NewBalancer(cfg *config.Config, middlewareExecutor *middleware.Executor, proxyFunc proxy.ProxyFunc) LoadBalancer {
	newBalancer, ok := balancers[cfg.Type]
	if !ok {
		zap.S().Errorf("Unknown balancer type %q", cfg.Type)
		return nil
	}

	poolAndBalancer := &loadBalancer{newBackendProxy: proxyFunc, customHeaders: cfg.CustomHeaders, middlewareExecutor: middlewareExecutor}
	backends := make([]*pool.Backend, 0, len(cfg.Backends))
	for i := range cfg.Backends {
		backend := poolAndBalancer.newBackend(&cfg.Backends[i])
		backend.Index = i
		backends = append(backends, backend)
	}

	poolAndBalancer.balancer = newBalancer(cfg, backends)
	poolAndBalancer.pool = pool.NewPool(backends, cfg.HealthCheckerFunc, cfg.HealthCheckerTime, poolAndBalancer.balancer)
	if poolAndBalancer.pool.AliveBackendCount() == 0 {
		zap.S().Warn("No Backend is Alive at startup; serving 503 until a Probe succeeds")
	}
	poolAndBalancer.pool.StartHealthChecker()

	return poolAndBalancer
}

func (l *loadBalancer) newBackend(backendConfig *config.Backend) *pool.Backend {
	return &pool.Backend{
		Addr:                backendConfig.Url,
		Weight:              backendConfig.Weight,
		ProbeURL:            backendConfig.GetHealthCheckURL(),
		ExpectedProbeStatus: backendConfig.HealthCheckStatus,
		Proxy:               l.newBackendProxy(backendConfig, l.customHeaders, l.middlewareExecutor),
	}
}

// ApplyReload relies on the Pool listing its Backends in the running config's
// order, which holds while Reloads do not overlap.
func (l *loadBalancer) ApplyReload(plan *config.ReloadPlan) {
	nextConfig := plan.NextConfig
	runningBackends := l.pool.Backends()
	nextBackends := make([]*pool.Backend, len(nextConfig.Backends))
	for _, kept := range plan.KeptBackends {
		nextBackends[kept.NextPosition] = runningBackends[kept.RunningPosition]
	}
	for _, nextPosition := range plan.AddedBackendNextPositions {
		nextBackends[nextPosition] = l.newBackend(&nextConfig.Backends[nextPosition])
	}

	// Setting them restarts the Probe loop's timer: frequent Reloads would starve its rounds.
	if plan.ProbeSettingsChanged {
		l.pool.SetProbeSettings(nextConfig.HealthCheckerFunc, nextConfig.HealthCheckerTime)
	}
	l.pool.ReplaceBackends(nextBackends)
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
