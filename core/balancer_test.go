package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func balancerConfig(balancerType string, probe types.IsHostAlive, addrs ...string) *config.Config {
	cfg := &config.Config{
		Type:              balancerType,
		HealthCheckerTime: time.Hour,
		HealthCheckerFunc: probe,
		HashFunc:          helper.HashFunc,
	}
	for _, addr := range addrs {
		cfg.Backends = append(cfg.Backends, config.Backend{Url: addr, Weight: 1})
	}
	return cfg
}

// backendsOf reaches the Backends NewBalancer built, in config order.
func backendsOf(b types.IBalancer) []*pool.Backend { return b.(*loadBalancer).pool.Backends() }

// probeAllBackends runs one Probe round now instead of waiting for the loop.
func probeAllBackends(b types.IBalancer) { b.(*loadBalancer).pool.ProbeAllBackends() }

func alwaysAlive(string, types.ExpectedProbeStatus) bool { return true }

func TestNewBalancerBuildsEveryType(t *testing.T) {
	for _, balancerType := range config.ValidTypes {
		t.Run(balancerType, func(t *testing.T) {
			cfg := balancerConfig(balancerType, alwaysAlive, "localhost:8080", "localhost:8081")
			b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
			if !assert.NotNil(t, b) {
				return
			}
			defer b.Shutdown() //nolint:errcheck

			stats := b.Stats()
			if assert.Len(t, stats, 2) {
				assert.Equal(t, "localhost:8080", stats[0].Addr)
				assert.Equal(t, "localhost:8081", stats[1].Addr)
				assert.True(t, stats[0].IsHostAlive)
				assert.True(t, stats[1].IsHostAlive)
			}
		})
	}
}

func TestNewBalancerServes503UntilAProbeSucceedsWhenNothingIsAliveAtStartup(t *testing.T) {
	for _, balancerType := range config.ValidTypes {
		t.Run(balancerType, func(t *testing.T) {
			var probeSucceeds atomic.Bool
			probe := func(string, types.ExpectedProbeStatus) bool { return probeSucceeds.Load() }
			cfg := balancerConfig(balancerType, probe, "localhost:8080")
			b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
			if !assert.NotNil(t, b, "divisor boots even when every Backend fails its first Probe round") {
				return
			}
			defer b.Shutdown() //nolint:errcheck

			ctx := mocks.RequestFrom("10.0.0.1")
			b.Serve()(ctx)
			assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
			assert.False(t, b.Stats()[0].IsHostAlive)

			probeSucceeds.Store(true)
			probeAllBackends(b)
			ctx = mocks.RequestFrom("10.0.0.1")
			b.Serve()(ctx)
			assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), "a Backend that Rejoins after a zero-Alive boot serves traffic")
		})
	}
}

func TestNewBalancerIsNilForUnknownType(t *testing.T) {
	cfg := balancerConfig("least-something", alwaysAlive, "localhost:8080")
	assert.Nil(t, NewBalancer(cfg, nil, mocks.CreateNewMockProxy))
}

func TestServeForwardsToThePickedBackend(t *testing.T) {
	cfg := balancerConfig("round-robin", alwaysAlive, "localhost:8080")
	b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck

	ctx := mocks.RequestFrom("10.0.0.1")
	b.Serve()(ctx)

	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Equal(t, "localhost:8080", b.Stats()[0].Addr)
	assert.True(t, mocks.MockProxyOf(backendsOf(b)[0]).IsCalled, "the request reached the picked Backend")
}

func TestNewBalancerProbesWithEachBackendsHealthCheckStatus(t *testing.T) {
	probes := &mocks.ProbeTable{}
	cfg := balancerConfig("round-robin", probes.Probe, "localhost:8080", "localhost:8081")
	cfg.Backends[0].HealthCheckStatus = fasthttp.StatusNoContent
	b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck

	assert.False(t, b.Stats()[0].IsHostAlive, "a 200 is a failed Probe when health_check_status is 204")
	assert.True(t, b.Stats()[1].IsHostAlive)
}

func TestServeAnswers503WhenNothingIsAlive(t *testing.T) {
	probes := &mocks.ProbeTable{}
	cfg := balancerConfig("round-robin", probes.Probe, "localhost:8080")
	b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck

	probes.SetAlive(backendsOf(b)[0], false)
	probeAllBackends(b)

	ctx := mocks.RequestFrom("10.0.0.1")
	b.Serve()(ctx)
	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
	assert.False(t, b.Stats()[0].IsHostAlive)

	probes.SetAlive(backendsOf(b)[0], true)
	probeAllBackends(b)
	ctx = mocks.RequestFrom("10.0.0.1")
	b.Serve()(ctx)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), "a Rejoined Backend serves again")
}

// Run with -race: every balancer's Pick runs on request goroutines while a
// Probe round Joins and Leaves Backends.
func TestPickConcurrentWithProbeRound(t *testing.T) {
	for balancerType, newBalancer := range balancers {
		t.Run(balancerType, func(t *testing.T) {
			backends := mocks.NewBackends(3)
			backends[0].Weight = 2
			cfg := balancerConfig(balancerType, nil, "localhost:8080", "localhost:8081", "localhost:8082")
			picker := newBalancer(cfg)

			var flapping atomic.Bool
			alive := func(url string, _ types.ExpectedProbeStatus) bool {
				return url != backends[0].ProbeURL || !flapping.Load()
			}
			pool := pool.NewPool(backends, alive, time.Hour, picker)
			defer pool.Shutdown() //nolint:errcheck

			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := 0; i < 300; i++ {
					flapping.Store(true)
					pool.ProbeAllBackends()
					flapping.Store(false)
					pool.ProbeAllBackends()
				}
			}()

			ctx := mocks.RequestFrom("10.1.2.3")
			for {
				select {
				case <-done:
					return
				default:
					if b := picker.Pick(ctx); b != nil {
						assert.Contains(t, backends, b)
					}
				}
			}
		})
	}
}

// running: 8080, 8081 → next: 8081, 8082. 8081 is kept, 8080 removed, 8082 added.
func replaceFirstBackendPlan(balancerType string, nextProbe types.IsHostAlive) *config.ReloadPlan {
	nextConfig := balancerConfig(balancerType, nextProbe, "localhost:8081", "localhost:8082")
	return &config.ReloadPlan{
		NextConfig:                     nextConfig,
		KeptBackends:                   []config.KeptBackend{{RunningPosition: 1, NextPosition: 0}},
		AddedBackendNextPositions:      []int{1},
		RemovedBackendRunningPositions: []int{0},
	}
}

func TestApplyReloadRoutesToTheNextBackendSet(t *testing.T) {
	for _, balancerType := range config.ValidTypes {
		t.Run(balancerType, func(t *testing.T) {
			b := NewBalancer(balancerConfig(balancerType, alwaysAlive, "localhost:8080", "localhost:8081"), nil, mocks.CreateNewMockProxy)
			defer b.Shutdown() //nolint:errcheck
			removed, kept := backendsOf(b)[0], backendsOf(b)[1]

			b.ApplyReload(replaceFirstBackendPlan(balancerType, alwaysAlive))

			stats := b.Stats()
			if assert.Len(t, stats, 2) {
				assert.Equal(t, "localhost:8081", stats[0].Addr)
				assert.Equal(t, "localhost:8082", stats[1].Addr)
				assert.True(t, stats[1].IsHostAlive, "the added Backend passed its Probe")
			}
			assert.Same(t, kept, backendsOf(b)[0], "the kept Backend is the same Backend, stats and all")
			assert.True(t, mocks.MockProxyOf(removed).CloseCalled, "the removed Backend's connections close")

			mocks.MockProxyOf(kept).ResTime = 5 // measured, so least-response-time prefers the unmeasured added Backend
			for i := 0; i < 200; i++ {
				ctx := mocks.RequestFrom(fmt.Sprintf("10.0.%d.%d", i/250, i%250))
				b.Serve()(ctx)
				assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
			}
			assert.False(t, mocks.MockProxyOf(removed).IsCalled, "nothing routes to a removed Backend")
			assert.True(t, mocks.MockProxyOf(backendsOf(b)[1]).IsCalled, "traffic reaches the added Backend")
		})
	}
}

func TestApplyReloadProbesAddedBackendsWithTheNextProbeSettings(t *testing.T) {
	b := NewBalancer(balancerConfig("round-robin", alwaysAlive, "localhost:8080", "localhost:8081"), nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck
	alwaysDown := func(string, types.ExpectedProbeStatus) bool { return false }
	changingProbeSettings := replaceFirstBackendPlan("round-robin", alwaysDown)
	changingProbeSettings.ProbeSettingsChanged = true

	b.ApplyReload(changingProbeSettings)

	assert.True(t, b.Stats()[0].IsHostAlive, "a kept Backend keeps its liveness until its next Probe")
	assert.False(t, b.Stats()[1].IsHostAlive, "the added Backend is Probed with the next config's Probe")
	probeAllBackends(b)
	assert.False(t, b.Stats()[0].IsHostAlive, "later Probe rounds use the next config's Probe")
}

func TestReloadsLeavingProbeSettingsAloneKeepTheProbeSchedule(t *testing.T) {
	var probeCount atomic.Int64
	countingProbe := func(string, types.ExpectedProbeStatus) bool { probeCount.Add(1); return true }
	cfg := balancerConfig("round-robin", countingProbe, "localhost:8080")
	cfg.HealthCheckerTime = 50 * time.Millisecond
	b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck
	probesAtBoot := probeCount.Load()

	keepEverything := &config.ReloadPlan{NextConfig: cfg, KeptBackends: []config.KeptBackend{{RunningPosition: 0, NextPosition: 0}}}
	for reloadingUntil := time.Now().Add(500 * time.Millisecond); time.Now().Before(reloadingUntil); {
		b.ApplyReload(keepEverything)
		time.Sleep(10 * time.Millisecond)
	}

	assert.Greater(t, probeCount.Load(), probesAtBoot, "Reloads more frequent than health_checker_time must not starve the Probe rounds")
}

func pickedAddress(balancer types.IBalancer, ip string) string {
	return balancer.(*loadBalancer).balancer.Pick(mocks.RequestFrom(ip)).Addr
}

// divisor instances behind one Service share a config but not a Reload
// history; ip-hash affinity needs both to route each Client IP to one address.
func TestIPHashRoutesLikeAFreshBootAfterAReload(t *testing.T) {
	reloaded := NewBalancer(balancerConfig(config.IPHashType, alwaysAlive, "localhost:8080", "localhost:8081", "localhost:8082"), nil, mocks.CreateNewMockProxy)
	defer reloaded.Shutdown() //nolint:errcheck
	reloaded.ApplyReload(&config.ReloadPlan{
		NextConfig:                     balancerConfig(config.IPHashType, alwaysAlive, "localhost:8080", "localhost:8082", "localhost:8083"),
		KeptBackends:                   []config.KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 2, NextPosition: 1}},
		AddedBackendNextPositions:      []int{2},
		RemovedBackendRunningPositions: []int{1},
	})
	booted := NewBalancer(balancerConfig(config.IPHashType, alwaysAlive, "localhost:8080", "localhost:8082", "localhost:8083"), nil, mocks.CreateNewMockProxy)
	defer booted.Shutdown() //nolint:errcheck

	for i := 0; i < 1000; i++ {
		ip := fmt.Sprintf("10.%d.%d.%d", i/65536, i/256%256, i%256)
		assert.Equal(t, pickedAddress(booted, ip), pickedAddress(reloaded, ip), "Client IP %s", ip)
	}
}

// A Backend killed and restarted at its address while Down: the keep-alive
// connections divisor pooled before the kill are dead, and a request retrying
// on them ran out of attempts and got 502.
func TestARestartedBackendAnswersTheFirstRequestAfterItRejoins(t *testing.T) {
	const pooledConnectionCount = 8
	var arrivedRequests atomic.Int64
	releaseRequests := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		arrivedRequests.Add(1)
		<-releaseRequests
	}))
	defer backend.Close()

	var probeSucceeds atomic.Bool
	probeSucceeds.Store(true)
	probe := func(string, types.ExpectedProbeStatus) bool { return probeSucceeds.Load() }
	b := NewBalancer(balancerConfig("round-robin", probe, backend.Listener.Addr().String()), nil, proxy.NewProxyClient)
	defer b.Shutdown() //nolint:errcheck
	serve := b.Serve()

	var concurrentRequests sync.WaitGroup
	for range pooledConnectionCount {
		concurrentRequests.Go(func() { serve(mocks.RequestFrom("10.0.0.1")) })
	}
	require.Eventually(t, func() bool { return arrivedRequests.Load() == pooledConnectionCount },
		5*time.Second, time.Millisecond, "every concurrent request holds its own Backend connection")
	close(releaseRequests)
	concurrentRequests.Wait()

	backend.CloseClientConnections()
	probeSucceeds.Store(false)
	probeAllBackends(b)
	probeSucceeds.Store(true)
	probeAllBackends(b)
	require.True(t, b.Stats()[0].IsHostAlive)

	ctx := mocks.RequestFrom("10.0.0.1")
	serve(ctx)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), "the Rejoined Backend is reached over a new connection")
}

// One failure scores a Backend at seconds, and least-response-time only
// re-measures a Backend it picks: without a trial request it never came back.
func TestLeastResponseTimePicksABackendAgainAfterOneFailure(t *testing.T) {
	var fasterRequests, slowerRequests atomic.Int64
	faster := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		if fasterRequests.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond)
			return
		}
		// Measurable on a coarse clock (Windows): a 0 sample reads as unmeasured.
		time.Sleep(5 * time.Millisecond)
	}))
	defer faster.Close()
	slower := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		slowerRequests.Add(1)
		time.Sleep(50 * time.Millisecond)
	}))
	defer slower.Close()

	cfg := balancerConfig(config.LeastResponseTimeType, alwaysAlive, faster.Listener.Addr().String(), slower.Listener.Addr().String())
	for i := range cfg.Backends {
		cfg.Backends[i].ProxyTimeout = 100 * time.Millisecond
	}
	b := NewBalancer(cfg, nil, proxy.NewProxyClient)
	defer b.Shutdown() //nolint:errcheck
	serve := b.Serve()

	// Until both are measured, the draw is random: the first request to the
	// faster Backend outlives proxy_timeout, the others it gets take 5ms.
	for request := 0; fasterRequests.Load() == 0 || slowerRequests.Load() == 0; request++ {
		require.Less(t, request, 100, "both Backends get a request from a cold start")
		serve(mocks.RequestFrom("10.0.0.1"))
	}
	fasterRequestsBeforeBothMeasured := fasterRequests.Load()

	for range 5 {
		serve(mocks.RequestFrom("10.0.0.1"))
	}
	require.Equal(t, fasterRequestsBeforeBothMeasured, fasterRequests.Load(), "its failure scored it at seconds, so the slower Backend took the rest")

	probeRounds := 0
	for fasterRequests.Load() == fasterRequestsBeforeBothMeasured && probeRounds < 10 {
		probeAllBackends(b)
		probeRounds++
		serve(mocks.RequestFrom("10.0.0.1"))
	}
	assert.Equal(t, 4, probeRounds, "the round that saw its last pick, then three rounds without one")

	for range 5 {
		serve(mocks.RequestFrom("10.0.0.1"))
	}
	assert.Equal(t, fasterRequestsBeforeBothMeasured+6, fasterRequests.Load(), "its trial request answered at once, so it is the fastest again")
}

// At a cold start every Backend is unmeasured, and picking the lowest Score
// alone sent a whole burst to the first one before it could answer.
func TestLeastResponseTimeSpreadsAConcurrentBurstFromAColdStart(t *testing.T) {
	newCountingBackend := func(requests *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			requests.Add(1)
			time.Sleep(50 * time.Millisecond)
		}))
	}
	var firstRequests, secondRequests atomic.Int64
	first := newCountingBackend(&firstRequests)
	defer first.Close()
	second := newCountingBackend(&secondRequests)
	defer second.Close()

	b := NewBalancer(balancerConfig(config.LeastResponseTimeType, alwaysAlive, first.Listener.Addr().String(), second.Listener.Addr().String()), nil, proxy.NewProxyClient)
	defer b.Shutdown() //nolint:errcheck
	serve := b.Serve()

	var concurrentRequests sync.WaitGroup
	for range 100 {
		concurrentRequests.Go(func() { serve(mocks.RequestFrom("10.0.0.1")) })
	}
	concurrentRequests.Wait()

	require.Equal(t, int64(100), firstRequests.Load()+secondRequests.Load(), "every request reached a Backend")
	assert.InDelta(t, 50, firstRequests.Load(), 25, "two equal Backends share the burst")
	assert.InDelta(t, 50, secondRequests.Load(), 25, "two equal Backends share the burst")
}
