package pool

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

// Balancer picks a Backend for each request from the ones the Pool handed it. Join and Leave come from one writer at a
// time (a Probe round or a Reload); Pick runs on every request goroutine and returns nil when nothing is Alive.
type Balancer interface {
	Join(*Backend)
	Leave(*Backend)
	Pick(ctx *fasthttp.RequestCtx) *Backend
}

// Pool owns every configured Backend and its liveness: it runs the Probes,
// moves Backends between Alive and Down, and tells the Balancer what it may
// pick from.
type Pool struct {
	balancer                   Balancer
	stopHealthChecker          chan struct{}
	healthCheckerDone          chan struct{}
	healthCheckIntervalChanged chan struct{}
	backends                   atomic.Pointer[[]*Backend]
	healthCheckInterval        atomic.Int64
	aliveBackendCount          atomic.Int64
	healthCheckerStarted       atomic.Bool
	startOnce                  sync.Once
	stopOnce                   sync.Once

	// transitionsMu makes Probe rounds and Backend set changes the Balancer's
	// one writer; it guards the fields below.
	transitionsMu    sync.Mutex
	probe            types.IsHostAlive
	nextBackendIndex int
}

// NewPool Probes every Backend once, synchronously, and Joins the Alive ones;
// the periodic Probe loop does not run until StartHealthChecker.
func NewPool(backends []*Backend, probe types.IsHostAlive, healthCheckInterval time.Duration, b Balancer) *Pool {
	pool := &Pool{
		probe:                      probe,
		balancer:                   b,
		nextBackendIndex:           len(backends),
		stopHealthChecker:          make(chan struct{}),
		healthCheckerDone:          make(chan struct{}),
		healthCheckIntervalChanged: make(chan struct{}, 1),
	}
	pool.backends.Store(&backends)
	pool.healthCheckInterval.Store(int64(healthCheckInterval))

	for _, backend := range backends {
		pool.probeAndJoinNewBackend(backend)
	}

	return pool
}

// Backends returns every registered Backend in config order; callers must not
// mutate the slice.
func (p *Pool) Backends() []*Backend {
	return *p.backends.Load()
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
	ticker := time.NewTicker(p.currentHealthCheckInterval())
	defer ticker.Stop()

	for {
		select {
		case <-p.stopHealthChecker:
			return
		case <-p.healthCheckIntervalChanged:
			ticker.Reset(p.currentHealthCheckInterval())
		case <-ticker.C:
			p.ProbeAllBackends()
		}
	}
}

func (p *Pool) currentHealthCheckInterval() time.Duration {
	return time.Duration(p.healthCheckInterval.Load())
}

// ProbeAllBackends Probes every Backend and applies the liveness transitions.
// The Probe loop and tests share it.
func (p *Pool) ProbeAllBackends() {
	p.transitionsMu.Lock()
	defer p.transitionsMu.Unlock()
	for _, backend := range p.Backends() {
		if helper.IsClosed(p.stopHealthChecker) {
			return
		}
		p.updateLiveness(backend, p.probe(backend.ProbeURL, backend.ExpectedProbeStatus))
	}
}

// SetProbeSettings replaces the Probe and the Probe loop interval; the next
// Probe (a round, or a Backend added by ReplaceBackends) uses them.
func (p *Pool) SetProbeSettings(probe types.IsHostAlive, healthCheckInterval time.Duration) {
	p.transitionsMu.Lock()
	p.probe = probe
	p.transitionsMu.Unlock()

	p.healthCheckInterval.Store(int64(healthCheckInterval))
	select {
	case p.healthCheckIntervalChanged <- struct{}{}:
	default:
	}
}

// ReplaceBackends makes backendsInConfigOrder the registered set (a Reload): survivors keep their Index, liveness
// and stats, added Backends get a never-used Index and a Probe, removed ones Leave and close. A no-op after Shutdown.
func (p *Pool) ReplaceBackends(backendsInConfigOrder []*Backend) {
	p.transitionsMu.Lock()
	defer p.transitionsMu.Unlock()
	if helper.IsClosed(p.stopHealthChecker) {
		return
	}

	runningBackends := p.Backends()
	for _, backend := range backendsInConfigOrder {
		if slices.Contains(runningBackends, backend) {
			continue
		}
		backend.Index = p.nextBackendIndex
		p.nextBackendIndex++
		p.probeAndJoinNewBackend(backend)
	}

	// Removed Backends Leave only after the added ones Joined, so replacing
	// every Backend never empties the rotation in between.
	nextBackends := slices.Clone(backendsInConfigOrder)
	p.backends.Store(&nextBackends)
	for _, backend := range runningBackends {
		if !slices.Contains(nextBackends, backend) {
			p.removeBackend(backend)
		}
	}
}

func (p *Pool) probeAndJoinNewBackend(backend *Backend) {
	if !p.probe(backend.ProbeURL, backend.ExpectedProbeStatus) {
		zap.S().Warnf("Backend is Down, it will join the rotation once a Probe succeeds, Addr: %s", backend.Addr)
		return
	}
	p.joinRotation(backend)
	zap.S().Infof("Backend is Alive and in rotation, Addr: %s", backend.Addr)
}

func (p *Pool) removeBackend(backend *Backend) {
	if backend.isAlive.Load() {
		p.leaveRotation(backend)
	}
	closeConnections(backend)
	zap.S().Infof("Backend removed from the Pool, Addr: %s", backend.Addr)
}

func (p *Pool) updateLiveness(backend *Backend, isAlive bool) {
	if isAlive == backend.isAlive.Load() {
		return
	}

	if isAlive {
		// A Rejoining Backend starts unmeasured: its score from before it
		// went Down — possibly a failure penalty — says nothing about it now.
		backend.Proxy.ResetRecentResponseTime()
		p.joinRotation(backend)
		zap.S().Infof("Backend is Alive again, Rejoining rotation, Addr: %s", backend.Addr)
		return
	}

	zap.S().Infof("Backend is Down, leaving rotation, Addr: %s", backend.Addr)
	p.leaveRotation(backend)
}

func (p *Pool) joinRotation(backend *Backend) {
	p.balancer.Join(backend)
	backend.isAlive.Store(true)
	p.aliveBackendCount.Add(1)
}

func (p *Pool) leaveRotation(backend *Backend) {
	p.balancer.Leave(backend)
	backend.isAlive.Store(false)
	if p.aliveBackendCount.Add(-1) == 0 {
		zap.S().Warn("All backends are down, serving 503 until a backend rejoins")
	}
}

func closeConnections(backend *Backend) {
	if err := backend.Proxy.Close(); err != nil {
		zap.S().Errorf("Error closing proxy connection: %s", err)
	}
}

func (p *Pool) Stats() []types.ProxyStat {
	backends := p.Backends()
	stats := make([]types.ProxyStat, len(backends))
	for i, backend := range backends {
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

	for _, backend := range p.Backends() {
		closeConnections(backend)
	}
	return nil
}
