package balancer

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

// SwappableBalancer wraps IBalancer with atomic swap capability for hot-reload
type SwappableBalancer struct {
	current atomic.Value // stores types.IBalancer
	mu      sync.RWMutex // protects swap operation
}

// NewSwappableBalancer creates a new swappable balancer wrapper
func NewSwappableBalancer(initial types.IBalancer) *SwappableBalancer {
	sb := &SwappableBalancer{}
	sb.current.Store(initial)
	return sb
}

// Serve implements types.IBalancer interface
// Uses atomic.Load for lock-free reads on hot path
func (sb *SwappableBalancer) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		balancer := sb.current.Load().(types.IBalancer)
		balancer.Serve()(ctx)
	}
}

// Stats implements types.IBalancer interface
func (sb *SwappableBalancer) Stats() []types.ProxyStat {
	balancer := sb.current.Load().(types.IBalancer)
	return balancer.Stats()
}

// Shutdown implements types.IBalancer interface
func (sb *SwappableBalancer) Shutdown() error {
	balancer := sb.current.Load().(types.IBalancer)
	return balancer.Shutdown()
}

// Swap atomically replaces the current balancer with a new one
// Gracefully shuts down old balancer after 5s delay
func (sb *SwappableBalancer) Swap(newBalancer types.IBalancer) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	oldBalancer := sb.current.Load().(types.IBalancer)

	// Atomic swap
	sb.current.Store(newBalancer)
	zap.S().Info("Balancer swapped successfully")

	// Gracefully drain old balancer
	go func() {
		// Allow in-flight requests to complete (5s)
		time.Sleep(5 * time.Second)

		zap.S().Info("Shutting down old balancer...")
		if err := oldBalancer.Shutdown(); err != nil {
			zap.S().Errorf("Error shutting down old balancer: %s", err)
		} else {
			zap.S().Info("Old balancer shutdown completed")
		}
	}()

	return nil
}

// Current returns the current balancer (for testing/debugging)
func (sb *SwappableBalancer) Current() types.IBalancer {
	return sb.current.Load().(types.IBalancer)
}
