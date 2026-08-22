package pool

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

// Balancer picks a Backend for each request from the ones the Pool has handed
// it. Join and Leave are called only from the Probe loop (one writer); Pick
// runs on every request goroutine and returns nil when nothing is Alive.
type Balancer interface {
	Join(*Backend)
	Leave(*Backend)
	Pick(ctx *fasthttp.RequestCtx) *Backend
}

// Pool owns every configured Backend and its liveness: it runs the Probes,
// moves Backends between Alive and Down, and tells the Balancer what it may
// pick from.
type Pool struct {
	probe                func(url string) bool
	balancer             Balancer
	stopHealthChecker    chan struct{}
	healthCheckerDone    chan struct{}
	backends             []*Backend
	healthCheckInterval  time.Duration
	aliveBackendCount    atomic.Int64
	healthCheckerStarted atomic.Bool
	startOnce            sync.Once
	stopOnce             sync.Once
}

// NewPool Probes every Backend once, synchronously, and Joins the Alive ones;
// the periodic Probe loop does not run until StartHealthChecker.
func NewPool(backends []*Backend, probe func(url string) bool, healthCheckInterval time.Duration, b Balancer) *Pool {
	pool := &Pool{
		backends:            backends,
		probe:               probe,
		healthCheckInterval: healthCheckInterval,
		balancer:            b,
		stopHealthChecker:   make(chan struct{}),
		healthCheckerDone:   make(chan struct{}),
	}

	for _, backend := range backends {
		if !pool.probe(backend.ProbeURL) {
			zap.S().Warnf("Backend is Down at startup, it will Rejoin once a Probe succeeds, Addr: %s", backend.Addr)
			continue
		}
		pool.balancer.Join(backend)
		backend.isAlive.Store(true)
		pool.aliveBackendCount.Add(1)
		zap.S().Infof("Backend is Alive and in rotation, Addr: %s", backend.Addr)
	}

	return pool
}

// Backends returns every registered Backend in config order.
func (p *Pool) Backends() []*Backend {
	return p.backends
}

func (p *Pool) AliveBackendCount() int {
	return int(p.aliveBackendCount.Load())
}

// StartHealthChecker runs a Probe round every healthCheckInterval until
// Shutdown.
func (p *Pool) StartHealthChecker() {
	p.startOnce.Do(func() {
		p.healthCheckerStarted.Store(true)
		go p.runHealthCheckLoop()
	})
}

func (p *Pool) runHealthCheckLoop() {
	defer close(p.healthCheckerDone)
	ticker := time.NewTicker(p.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopHealthChecker:
			return
		case <-ticker.C:
			p.ProbeAllBackends()
		}
	}
}

// ProbeAllBackends Probes every Backend and applies the liveness transitions. The
// Probe loop and tests share it; it must not run concurrently with itself.
func (p *Pool) ProbeAllBackends() {
	for _, backend := range p.backends {
		if helper.IsClosed(p.stopHealthChecker) {
			return
		}
		p.updateLiveness(backend, p.probe(backend.ProbeURL))
	}
}

func (p *Pool) updateLiveness(backend *Backend, isAlive bool) {
	if isAlive == backend.isAlive.Load() {
		return
	}

	if isAlive {
		// A Rejoining Backend starts unmeasured: its score from before it
		// went Down — possibly a failure penalty — says nothing about it now.
		backend.Proxy.ResetRecentResponseTime()
		p.balancer.Join(backend)
		backend.isAlive.Store(true)
		p.aliveBackendCount.Add(1)
		zap.S().Infof("Backend is Alive again, Rejoining rotation, Addr: %s", backend.Addr)
		return
	}

	p.balancer.Leave(backend)
	backend.isAlive.Store(false)
	zap.S().Infof("Backend is Down, leaving rotation, Addr: %s", backend.Addr)
	if p.aliveBackendCount.Add(-1) == 0 {
		zap.S().Warn("All backends are down, serving 503 until a backend rejoins")
	}
}

func (p *Pool) Stats() []types.ProxyStat {
	stats := make([]types.ProxyStat, len(p.backends))
	for i, backend := range p.backends {
		stat := backend.Proxy.Stat()
		stat.IsHostAlive = backend.isAlive.Load()
		stat.BackendHash = uint32(backend.Index)
		stats[i] = stat
	}
	return stats
}

// Shutdown stops the Probe loop, waits for a round in flight, then closes
// every Backend's connections. Safe to call more than once.
func (p *Pool) Shutdown() error {
	// Close rather than send: a send is dropped whenever the loop is not
	// parked in its select, which is most of the time.
	p.stopOnce.Do(func() { close(p.stopHealthChecker) })
	if p.healthCheckerStarted.Load() {
		select {
		case <-p.healthCheckerDone:
		case <-time.After(types.HealthCheckerStopTimeout):
			zap.S().Warn("Health checker did not stop in time, continuing shutdown")
		}
	}

	for _, backend := range p.backends {
		if err := backend.Proxy.Close(); err != nil {
			zap.S().Errorf("Error closing proxy connection: %s", err)
		}
	}
	return nil
}
