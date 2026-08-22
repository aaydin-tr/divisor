package pool_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/stretchr/testify/assert"
)

func TestNewPoolJoinsOnlyAliveBackends(t *testing.T) {
	backends := mocks.NewBackends(3)
	probes := &mocks.ProbeTable{}
	probes.SetAlive(backends[1], false)
	balancer := &mocks.RecordingBalancer{}

	p := pool.NewPool(backends, probes.Probe, time.Hour, balancer)
	defer p.Shutdown() //nolint:errcheck

	joins, leaves := balancer.Transitions()
	assert.Equal(t, []*pool.Backend{backends[0], backends[2]}, joins, "only Alive Backends Join at startup")
	assert.Empty(t, leaves)
	assert.Equal(t, 2, p.AliveBackendCount())
	assert.True(t, backends[0].IsAlive())
	assert.False(t, backends[1].IsAlive(), "a Backend Down at startup is registered Down")
	assert.True(t, backends[2].IsAlive())
}

func TestProbeAllBackendsAppliesOneTransitionPerFlip(t *testing.T) {
	backends := mocks.NewBackends(2)
	probes := &mocks.ProbeTable{}
	balancer := &mocks.RecordingBalancer{}
	p := pool.NewPool(backends, probes.Probe, time.Hour, balancer)
	defer p.Shutdown() //nolint:errcheck

	probes.SetAlive(backends[0], false)
	p.ProbeAllBackends()
	p.ProbeAllBackends()

	joins, leaves := balancer.Transitions()
	assert.Equal(t, []*pool.Backend{backends[0]}, leaves, "a Backend going Down Leaves exactly once")
	assert.Len(t, joins, 2, "unchanged Backends are not re-Joined")
	assert.False(t, backends[0].IsAlive())
	assert.Equal(t, 1, p.AliveBackendCount())

	probes.SetAlive(backends[0], true)
	p.ProbeAllBackends()
	p.ProbeAllBackends()

	joins, leaves = balancer.Transitions()
	assert.Equal(t, []*pool.Backend{backends[0], backends[1], backends[0]}, joins, "a Rejoining Backend Joins exactly once")
	assert.Len(t, leaves, 1)
	assert.True(t, backends[0].IsAlive())
	assert.Equal(t, 2, p.AliveBackendCount())
}

func TestRejoinResetsResponseTimeScore(t *testing.T) {
	backends := mocks.NewBackends(1)
	probes := &mocks.ProbeTable{}
	p := pool.NewPool(backends, probes.Probe, time.Hour, &mocks.RecordingBalancer{})
	defer p.Shutdown() //nolint:errcheck
	mocks.MockProxyOf(backends[0]).ResTime = 42

	probes.SetAlive(backends[0], false)
	p.ProbeAllBackends()
	assert.Equal(t, float64(42), mocks.MockProxyOf(backends[0]).ResTime, "going Down alone must not clear the score")

	probes.SetAlive(backends[0], true)
	p.ProbeAllBackends()
	assert.Equal(t, float64(0), mocks.MockProxyOf(backends[0]).ResTime, "a Rejoining Backend must start unmeasured")
}

func TestAllBackendsDownLeavesNothingToPick(t *testing.T) {
	backends := mocks.NewBackends(2)
	probes := &mocks.ProbeTable{}
	balancer := &mocks.RecordingBalancer{}
	p := pool.NewPool(backends, probes.Probe, time.Hour, balancer)
	defer p.Shutdown() //nolint:errcheck

	probes.SetAlive(backends[0], false)
	probes.SetAlive(backends[1], false)
	p.ProbeAllBackends()

	assert.Equal(t, 0, p.AliveBackendCount())
	assert.Nil(t, balancer.Pick(nil))

	probes.SetAlive(backends[1], true)
	p.ProbeAllBackends()
	assert.Same(t, backends[1], balancer.Pick(nil), "a Rejoined Backend is picked again")
}

func TestStatsFollowConfigOrderAndLiveness(t *testing.T) {
	backends := mocks.NewBackends(3)
	probes := &mocks.ProbeTable{}
	probes.SetAlive(backends[1], false)
	p := pool.NewPool(backends, probes.Probe, time.Hour, &mocks.RecordingBalancer{})
	defer p.Shutdown() //nolint:errcheck

	stats := p.Stats()
	if assert.Len(t, stats, 3) {
		for i, b := range backends {
			assert.Equal(t, b.Addr, stats[i].Addr)
			assert.Equal(t, uint32(i), stats[i].BackendHash, "BackendHash is the config index")
		}
		assert.True(t, stats[0].IsHostAlive)
		assert.False(t, stats[1].IsHostAlive, "a Backend Down at startup still has a stats row")
		assert.True(t, stats[2].IsHostAlive)
	}

	probes.SetAlive(backends[0], false)
	p.ProbeAllBackends()
	assert.False(t, p.Stats()[0].IsHostAlive)
}

func TestShutdownClosesEveryBackend(t *testing.T) {
	t.Run("closes all proxies, Alive or Down, and is idempotent", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		probes := &mocks.ProbeTable{}
		probes.SetAlive(backends[1], false)
		p := pool.NewPool(backends, probes.Probe, time.Hour, &mocks.RecordingBalancer{})

		assert.NoError(t, p.Shutdown())
		for _, b := range backends {
			assert.True(t, mocks.MockProxyOf(b).CloseCalled, "Close() must be called on %s", b.Addr)
		}
		assert.NoError(t, p.Shutdown(), "a second Shutdown is harmless")
	})

	t.Run("returns promptly when the loop was never started", func(t *testing.T) {
		p := pool.NewPool(mocks.NewBackends(1), (&mocks.ProbeTable{}).Probe, time.Hour, &mocks.RecordingBalancer{})
		start := time.Now()
		assert.NoError(t, p.Shutdown())
		assert.Less(t, time.Since(start), time.Second)
	})
}

func TestShutdownStopsHealthChecker(t *testing.T) {
	backends := mocks.NewBackends(2)
	var checks atomic.Int64
	probe := func(string) bool { checks.Add(1); return true }
	p := pool.NewPool(backends, probe, 5*time.Millisecond, &mocks.RecordingBalancer{})
	p.StartHealthChecker()

	assert.Eventually(t, func() bool { return checks.Load() > int64(len(backends)) },
		time.Second, time.Millisecond, "the Probe loop should run periodically once started")

	assert.NoError(t, p.Shutdown())

	afterShutdown := checks.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, afterShutdown, checks.Load(), "Probe loop kept running after Shutdown")
}

// Run with -race: Stats() reads each Backend's liveness while a Probe round
// flips it.
func TestStatsConcurrentWithProbeRound(t *testing.T) {
	backends := mocks.NewBackends(1)
	var alive atomic.Bool
	alive.Store(true)
	p := pool.NewPool(backends, func(string) bool { return alive.Load() }, time.Hour, &mocks.RecordingBalancer{})
	defer p.Shutdown() //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			alive.Store(false)
			p.ProbeAllBackends()
			alive.Store(true)
			p.ProbeAllBackends()
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			p.Stats()
		}
	}
}
