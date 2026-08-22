package core

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/stretchr/testify/assert"
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

func alwaysAlive(string) bool { return true }
func alwaysDown(string) bool  { return false }

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

func TestNewBalancerIsNilWhenNothingIsAliveAtStartup(t *testing.T) {
	cfg := balancerConfig("round-robin", alwaysDown, "localhost:8080")
	assert.Nil(t, NewBalancer(cfg, nil, mocks.CreateNewMockProxy))
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

func TestServeAnswers503WhenNothingIsAlive(t *testing.T) {
	probes := &mocks.ProbeTable{}
	cfg := balancerConfig("round-robin", probes.Probe, "localhost:8080")
	b := NewBalancer(cfg, nil, mocks.CreateNewMockProxy)
	defer b.Shutdown() //nolint:errcheck
	lb := b.(*loadBalancer)

	probes.SetAlive(backendsOf(b)[0], false)
	lb.pool.ProbeAllBackends()

	ctx := mocks.RequestFrom("10.0.0.1")
	b.Serve()(ctx)
	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
	assert.False(t, b.Stats()[0].IsHostAlive)

	probes.SetAlive(backendsOf(b)[0], true)
	lb.pool.ProbeAllBackends()
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
			picker := newBalancer(cfg, backends)

			var flapping atomic.Bool
			alive := func(url string) bool { return url != backends[0].ProbeURL || !flapping.Load() }
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
