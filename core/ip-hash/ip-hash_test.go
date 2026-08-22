package ip_hash

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewIPHash(t *testing.T) {
	for _, ip := range mocks.TestCases {
		if ip.ExpectedServerCount == 0 {
			ipHash := NewIPHash(&ip.Config, nil, ip.ProxyFunc)
			assert.Nil(t, ipHash)
		} else {
			ipHash := NewIPHash(&ip.Config, nil, ip.ProxyFunc).(*IPHash)
			assert.Equal(t, ip.ExpectedServerCount, len(ipHash.serversMap))
		}
	}
}

func TestGet(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	proxy := ipHash.get(caseOne.Config.HashFunc([]byte{1, 2, 3}))

	assert.IsType(t, &mocks.MockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	handlerFunc := ipHash.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	proxy := ipHash.get(caseOne.Config.HashFunc([]byte{1})).(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	stats := ipHash.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := ipHash.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := ipHash.serversMap[hash]

		assert.Equal(t, s.node.Addr, stats[i].Addr)
		assert.Equal(t, hash, stats[i].BackendHash)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	ipHash := &IPHash{
		stopHealthChecker: make(chan struct{}),
		healthCheckerDone: make(chan struct{}),
		healthCheckerTime: time.Millisecond,
	}

	var stopOnce sync.Once
	ipHash.isHostAlive = func(s string) bool {
		stopOnce.Do(func() { close(ipHash.stopHealthChecker) })
		return false
	}
	ipHash.hashFunc = func(b []byte) uint32 {
		return 0
	}

	ipHash.healthChecker(caseOne.Config.Backends)
	assert.True(t, helper.IsClosed(ipHash.healthCheckerDone), "healthChecker should signal completion on return")
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	assert.Equal(t, caseOne.ExpectedServerCount, len(ipHash.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := ipHash.serversMap[ipHash.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		ipHash.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := ipHash.len
		ipHash.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive.Load(), "expected isHostAlive equal to false, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, oldServerCount, ipHash.len, "expected server to be removed after health check, but it did not.")
	}

}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	assert.Equal(t, caseOne.ExpectedServerCount, len(ipHash.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := ipHash.serversMap[ipHash.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		ipHash.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := ipHash.len
		ipHash.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive.Load(), "expected isHostAlive equal to false, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, oldServerCount, ipHash.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := ipHash.serversMap[ipHash.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive.Store(false)
		ipHash.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := ipHash.len
		ipHash.healthCheck(&backend, 0)

		assert.True(t, b.isHostAlive.Load(), "expected isHostAlive equal to true, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, ipHash.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestAllBackendsDownStaysUp(t *testing.T) {
	// SPEC (1.0): losing the last live backend must not kill the process;
	// requests get 503 until a Probe lets a backend Rejoin.
	caseOne := mocks.TestCases[0]
	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	assert.Equal(t, caseOne.ExpectedServerCount, len(ipHash.serversMap))

	ipHash.isHostAlive = func(s string) bool {
		return false
	}
	for i, backend := range caseOne.Config.Backends {
		assert.NotPanics(t, func() {
			ipHash.healthCheck(&backend, i)
		}, "losing the last live backend must not panic")
	}
	assert.Equal(t, 0, ipHash.len)

	handler := ipHash.Serve()
	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())

	// Rejoin after the total outage.
	ipHash.isHostAlive = func(s string) bool {
		return true
	}
	ipHash.healthCheck(&caseOne.Config.Backends[0], 0)
	assert.Equal(t, 1, ipHash.len)

	ctx = fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
		assert.NotNil(t, ipHash)

		// Verify proxy Close() methods are not called yet
		for _, sm := range ipHash.serversMap {
			mockProxy := sm.node.Proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := ipHash.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range ipHash.serversMap {
			mockProxy := sm.node.Proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown with no servers", func(t *testing.T) {
		emptyCase := mocks.TestCases[3] // Case with 0 servers
		emptyIPHash := NewIPHash(&emptyCase.Config, nil, emptyCase.ProxyFunc)
		if emptyIPHash != nil {
			err := emptyIPHash.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
		assert.NotNil(t, ipHash)

		// Give health checker time to start
		time.Sleep(50 * time.Millisecond)

		// Call shutdown - this should stop the health checker goroutine
		err := ipHash.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range ipHash.serversMap {
			mockProxy := sm.node.Proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}

		// Give some time for health checker to actually stop
		time.Sleep(150 * time.Millisecond)
	})
}

func TestStatsWhenBackendDownAtStartup(t *testing.T) {
	cfg := config.Config{
		Backends: []config.Backend{
			{Url: "localhost:8080", Weight: 1},
			{Url: "localhost:80", Weight: 1},
		},
		HealthCheckerTime: time.Second * 5,
		HealthCheckerFunc: func(url string) bool {
			return url != "http://localhost:8080"
		},
		HashFunc: func(b []byte) uint32 {
			return uint32(len(b))
		},
	}

	balancer := NewIPHash(&cfg, nil, mocks.CreateNewMockProxy)
	assert.NotNil(t, balancer)

	stats := balancer.Stats()
	assert.Len(t, stats, 2)
	assert.Equal(t, "localhost:8080", stats[0].Addr)
	assert.False(t, stats[0].IsHostAlive)
	assert.Equal(t, "localhost:80", stats[1].Addr)
	assert.True(t, stats[1].IsHostAlive)
}

func TestBackendDownAtStartupCanRejoin(t *testing.T) {
	cfg := config.Config{
		Backends: []config.Backend{
			{Url: "localhost:8080", Weight: 1},
			{Url: "localhost:80", Weight: 1},
		},
		HealthCheckerTime: time.Second * 5,
		HealthCheckerFunc: func(url string) bool {
			return url != "http://localhost:8080"
		},
		HashFunc: func(b []byte) uint32 {
			return uint32(len(b))
		},
	}

	ipHash := NewIPHash(&cfg, nil, mocks.CreateNewMockProxy).(*IPHash)
	assert.Equal(t, 1, ipHash.len)

	ipHash.isHostAlive = func(string) bool { return true }
	ipHash.healthCheck(&cfg.Backends[0], 0)

	assert.Equal(t, 2, ipHash.len)
	sm := ipHash.serversMap[ipHash.hashFunc([]byte("localhost:8080"+"0"))]
	assert.True(t, sm.isHostAlive.Load())
}

func BenchmarkNext(b *testing.B) {
	caseOne := mocks.TestCases[0]
	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	hashCode := ipHash.hashFunc([]byte("192.168.1.1"))
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ipHash.get(hashCode)
		}
	})
}

func TestShutdownStopsHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.HealthCheckerTime = 5 * time.Millisecond

	var checks atomic.Int64
	caseOne.Config.HealthCheckerFunc = func(string) bool {
		checks.Add(1)
		return true
	}

	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	assert.NotNil(t, ipHash)

	assert.Eventually(t, func() bool { return checks.Load() > int64(len(caseOne.Config.Backends)) },
		time.Second, time.Millisecond, "health checker should run periodically")

	assert.NoError(t, ipHash.Shutdown())

	afterShutdown := checks.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, afterShutdown, checks.Load(), "health checker kept running after Shutdown")
}

// Run with -race: Stats() reads each Backend's liveness while the health
// checker flips it.
func TestStatsConcurrentWithHealthCheck(t *testing.T) {
	caseOne := mocks.TestCases[0]
	var alive atomic.Bool
	alive.Store(true)
	caseOne.Config.HealthCheckerFunc = func(string) bool { return alive.Load() }
	balancer := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	defer balancer.Shutdown() //nolint:errcheck

	backend := caseOne.Config.Backends[0]
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			alive.Store(false)
			balancer.healthCheck(&backend, 0)
			alive.Store(true)
			balancer.healthCheck(&backend, 0)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			balancer.Stats()
		}
	}
}

func TestDuplicateAddressTwinKeepsItsVirtualNodes(t *testing.T) {
	cfg := mocks.TestCases[0].Config
	cfg.HashFunc = helper.HashFunc
	cfg.Backends = []config.Backend{{Url: "localhost:8080"}, {Url: "localhost:8080"}}
	ipHash := NewIPHash(&cfg, nil, mocks.TestCases[0].ProxyFunc).(*IPHash)
	defer ipHash.Shutdown() //nolint:errcheck

	const samples = 1000
	const sampleStride = math.MaxUint32 / samples
	routeOf := func(i int) proxy.IProxyClient { return ipHash.get(uint32(i * sampleStride)) }
	before := make([]proxy.IProxyClient, samples)
	for i := range before {
		before[i] = routeOf(i)
	}

	first := cfg.Backends[0]
	twin := ipHash.serversMap[ipHash.hashFunc([]byte("localhost:8080"+strconv.Itoa(1)))]

	ipHash.isHostAlive = func(string) bool { return false }
	ipHash.healthCheck(&first, 0)
	assert.Equal(t, 1, ipHash.len)
	assert.True(t, twin.isHostAlive.Load())
	for i := 0; i < samples; i++ {
		assert.Same(t, twin.node.Proxy, routeOf(i), "hash %d must fail over to the twin", i)
	}

	ipHash.isHostAlive = func(string) bool { return true }
	ipHash.healthCheck(&first, 0)
	assert.Equal(t, 2, ipHash.len)
	for i := 0; i < samples; i++ {
		assert.Same(t, before[i], routeOf(i), "hash %d must route as it did before the flap", i)
	}
}
