package w_round_robin

import (
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

func TestNewWRoundRobin(t *testing.T) {
	for _, r := range mocks.TestCases {
		if r.ExpectedServerCount == 0 {
			wRoundRobin := NewWRoundRobin(&r.Config, nil, r.ProxyFunc)
			assert.Nil(t, wRoundRobin)
		} else {
			wRoundRobin := NewWRoundRobin(&r.Config, nil, r.ProxyFunc).(*WRoundRobin)
			assert.Equal(t, r.ExpectedServerCount, len(wRoundRobin.serversMap))
		}
	}
}

func TestNext(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	wRoundRobin := balancer.(*WRoundRobin)
	proxy := wRoundRobin.next()

	assert.IsType(t, &mocks.MockProxy{}, proxy)

	mockProxy := proxy.(*mocks.MockProxy)
	assert.Equal(t, caseOne.Config.Backends[0].Url, mockProxy.Addr)
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	wRoundRobin := balancer.(*WRoundRobin)
	handlerFunc := wRoundRobin.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	proxy := wRoundRobin.next().(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	wRoundRobin := balancer.(*WRoundRobin)
	stats := wRoundRobin.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := wRoundRobin.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := wRoundRobin.serversMap[hash]
		p := s.proxy.(*mocks.MockProxy)

		assert.Equal(t, s.isHostAlive.Load(), stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
		assert.Equal(t, backend.Url, p.Addr)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := &WRoundRobin{
		stopHealthChecker: make(chan struct{}),
		healthCheckerDone: make(chan struct{}),
		healthCheckerTime: time.Millisecond,
	}

	var stopOnce sync.Once
	wRoundRobin.isHostAlive = func(s string) bool {
		stopOnce.Do(func() { close(wRoundRobin.stopHealthChecker) })
		return false
	}
	wRoundRobin.hashFunc = func(b []byte) uint32 {
		return 0
	}

	wRoundRobin.healthChecker(caseOne.Config.Backends)
	assert.True(t, helper.IsClosed(wRoundRobin.healthCheckerDone), "healthChecker should signal completion on return")
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(wRoundRobin.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		wRoundRobin.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*wRoundRobin.servers.Load())
		wRoundRobin.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive.Load(), "expected isHostAlive equal to false, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, oldServerCount, len(*wRoundRobin.servers.Load()), "expected server to be removed after health check, but it did not.")
	}
}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(wRoundRobin.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		wRoundRobin.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*wRoundRobin.servers.Load())
		wRoundRobin.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive.Load(), "expected isHostAlive equal to false, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, oldServerCount, len(*wRoundRobin.servers.Load()), "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive.Store(false)
		wRoundRobin.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := len(*wRoundRobin.servers.Load())
		wRoundRobin.healthCheck(&backend, 0)

		assert.True(t, b.isHostAlive.Load(), "expected isHostAlive equal to true, but got %v", b.isHostAlive.Load())
		assert.GreaterOrEqual(t, len(*wRoundRobin.servers.Load()), oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestAllBackendsDownStaysUp(t *testing.T) {
	// SPEC (1.0): losing the last live backend must not kill the process;
	// requests get 503 until a Probe lets a backend Rejoin.
	caseOne := mocks.TestCases[0]
	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(wRoundRobin.serversMap))

	wRoundRobin.isHostAlive = func(s string) bool {
		return false
	}
	for i, backend := range caseOne.Config.Backends {
		assert.NotPanics(t, func() {
			wRoundRobin.healthCheck(&backend, i)
		}, "losing the last live backend must not panic")
	}
	assert.Empty(t, *wRoundRobin.servers.Load())

	handler := wRoundRobin.Serve()
	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())

	// Rejoin after the total outage.
	wRoundRobin.isHostAlive = func(s string) bool {
		return true
	}
	wRoundRobin.healthCheck(&caseOne.Config.Backends[0], 0)
	assert.Len(t, *wRoundRobin.servers.Load(), int(caseOne.Config.Backends[0].Weight))

	ctx = fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
		assert.NotNil(t, wRoundRobin)

		// Verify proxy Close() methods are not called yet
		for _, sm := range wRoundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := wRoundRobin.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range wRoundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown with no servers", func(t *testing.T) {
		emptyCase := mocks.TestCases[3] // Case with 0 servers
		emptyWRoundRobin := NewWRoundRobin(&emptyCase.Config, nil, emptyCase.ProxyFunc)
		if emptyWRoundRobin != nil {
			err := emptyWRoundRobin.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
		assert.NotNil(t, wRoundRobin)

		// Give health checker time to start
		time.Sleep(50 * time.Millisecond)

		// Call shutdown - this should stop the health checker goroutine
		err := wRoundRobin.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range wRoundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
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

	balancer := NewWRoundRobin(&cfg, nil, mocks.CreateNewMockProxy)
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
			{Url: "localhost:8080", Weight: 2},
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

	wRoundRobin := NewWRoundRobin(&cfg, nil, mocks.CreateNewMockProxy).(*WRoundRobin)
	assert.Len(t, *wRoundRobin.servers.Load(), 1)

	wRoundRobin.isHostAlive = func(string) bool { return true }
	wRoundRobin.healthCheck(&cfg.Backends[0], 0)

	assert.Len(t, *wRoundRobin.servers.Load(), 3)
	sm := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte("localhost:8080"+"0"))]
	assert.True(t, sm.isHostAlive.Load())
}

func TestNextConcurrentWithHealthCheck(t *testing.T) {
	hashFunc := func(b []byte) uint32 { return uint32(len(b)) }
	p1 := &mocks.MockProxy{Addr: "localhost:8080"}
	p2 := &mocks.MockProxy{Addr: "localhost:80"}

	var alive atomic.Bool
	wRoundRobin := &WRoundRobin{
		hashFunc:    hashFunc,
		isHostAlive: func(string) bool { return alive.Load() },
		serversMap: map[uint32]*serverMap{
			hashFunc([]byte("localhost:8080" + "0")): {proxy: p1, weight: 1, statsIdx: 0},
			hashFunc([]byte("localhost:80" + "1")):   {proxy: p2, weight: 1, statsIdx: 1},
		},
	}
	for _, sm := range wRoundRobin.serversMap {
		sm.isHostAlive.Store(true)
	}
	servers := []proxy.IProxyClient{p1, p2}
	wRoundRobin.servers.Store(&servers)

	backend := config.Backend{Url: "localhost:8080"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			alive.Store(false)
			wRoundRobin.healthCheck(&backend, 0)
			alive.Store(true)
			wRoundRobin.healthCheck(&backend, 0)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			wRoundRobin.next()
		}
	}
}

func BenchmarkNext(b *testing.B) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wRoundRobin.next()
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

	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	assert.NotNil(t, wRoundRobin)

	assert.Eventually(t, func() bool { return checks.Load() > int64(len(caseOne.Config.Backends)) },
		time.Second, time.Millisecond, "health checker should run periodically")

	assert.NoError(t, wRoundRobin.Shutdown())

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
	balancer := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
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
