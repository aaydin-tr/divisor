package least_algorithm

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewLeastAlgorithm(t *testing.T) {
	for i, l := range mocks.TestCases {
		if l.ExpectedServerCount == 0 {
			testConfig := l.Config
			testConfig.Type = "least-connection"
			if i%2 == 0 {
				testConfig.Type = "least-response-time"
			}

			leastAlgorithm := NewLeastAlgorithm(&testConfig, nil, l.ProxyFunc)
			assert.Nil(t, leastAlgorithm)
		} else {
			testConfig := l.Config
			testConfig.Type = "least-connection"
			if i%2 == 0 {
				testConfig.Type = "least-response-time"
			}

			leastAlgorithm := NewLeastAlgorithm(&testConfig, nil, l.ProxyFunc).(*LeastAlgorithm)
			assert.Equal(t, l.ExpectedServerCount, len(leastAlgorithm.serversMap))
			assert.Equal(t, l.ExpectedServerCount, len(*leastAlgorithm.servers.Load()))
		}
	}
}

func TestNewLeastAlgorithmWithoutAlgorithmType(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = ""
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.Nil(t, leastAlgorithm)
}

func TestNext(t *testing.T) {
	t.Run("least-connection", func(t *testing.T) {
		t.Run("with zero pending requests", func(t *testing.T) {
			caseFour := mocks.TestCases[4]
			caseFour.Config.Type = "least-connection"
			balancer := NewLeastAlgorithm(&caseFour.Config, nil, caseFour.ProxyFunc)
			assert.NotNil(t, balancer)

			leastConnection := balancer.(*LeastAlgorithm)
			proxy := leastConnection.nextFunc()

			assert.IsType(t, &mocks.MockProxy{}, proxy)
			mProxy := proxy.(*mocks.MockProxy)
			assert.Equal(t, caseFour.Config.Backends[0].Url, mProxy.Addr)
		})

		t.Run("with non zero pending requests", func(t *testing.T) {
			caseOne := mocks.TestCases[0]
			caseOne.Config.Type = "least-connection"
			balancer := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc)
			assert.NotNil(t, balancer)

			leastConnection := balancer.(*LeastAlgorithm)
			proxy := leastConnection.nextFunc()

			assert.IsType(t, &mocks.MockProxy{}, proxy)
			mProxy := proxy.(*mocks.MockProxy)
			assert.Equal(t, caseOne.Config.Backends[1].Url, mProxy.Addr)
		})
	})

	t.Run("least-response-time", func(t *testing.T) {
		t.Run("with zero avg response time", func(t *testing.T) {
			caseFive := mocks.TestCases[4]
			caseFive.Config.Type = "least-response-time"
			balancer := NewLeastAlgorithm(&caseFive.Config, nil, caseFive.ProxyFunc)
			assert.NotNil(t, balancer)

			leastResponseTime := balancer.(*LeastAlgorithm)
			proxy := leastResponseTime.nextFunc()

			assert.IsType(t, &mocks.MockProxy{}, proxy)
			mProxy := proxy.(*mocks.MockProxy)
			assert.Equal(t, caseFive.Config.Backends[1].Url, mProxy.Addr)
		})

		t.Run("with non zero avg response time", func(t *testing.T) {
			caseOne := mocks.TestCases[0]
			caseOne.Config.Type = "least-response-time"
			balancer := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc)
			assert.NotNil(t, balancer)

			leastResponseTime := balancer.(*LeastAlgorithm)
			proxy := leastResponseTime.nextFunc()

			assert.IsType(t, &mocks.MockProxy{}, proxy)
			mProxy := proxy.(*mocks.MockProxy)
			assert.Equal(t, caseOne.Config.Backends[0].Url, mProxy.Addr)

		})
	})
}

func TestServe(t *testing.T) {
	caseOne := mocks.TestCases[1]
	caseOne.Config.Type = "least-connection"
	balancer := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	leastAlgorithm := balancer.(*LeastAlgorithm)
	handlerFunc := leastAlgorithm.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	proxy := leastAlgorithm.nextFunc().(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	leastAlgorithm := &LeastAlgorithm{stopHealthChecker: make(chan bool)}

	leastAlgorithm.isHostAlive = func(s string) bool {
		go func() {
			leastAlgorithm.stopHealthChecker <- true
		}()
		return false
	}
	leastAlgorithm.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	leastAlgorithm.healthChecker(caseOne.Config.Backends)
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	balancer := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	leastAlgorithm := balancer.(*LeastAlgorithm)
	stats := leastAlgorithm.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := leastAlgorithm.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := leastAlgorithm.serversMap[hash]
		p := s.proxy.(*mocks.MockProxy)

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
		assert.Equal(t, backend.Url, p.Addr)
	}
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastAlgorithm.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*leastAlgorithm.servers.Load())
		leastAlgorithm.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, len(*leastAlgorithm.servers.Load()), "expected server to be removed after health check, but it did not.")
	}
}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastAlgorithm.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*leastAlgorithm.servers.Load())
		leastAlgorithm.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, len(*leastAlgorithm.servers.Load()), "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		leastAlgorithm.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := len(*leastAlgorithm.servers.Load())
		leastAlgorithm.healthCheck(&backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, len(*leastAlgorithm.servers.Load()), oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestAllBackendsDownStaysUp(t *testing.T) {
	// SPEC (1.0): losing the last live backend must not kill the process;
	// requests get 503 until a Probe lets a backend Rejoin.
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	leastAlgorithm.isHostAlive = func(s string) bool {
		return false
	}
	for i, backend := range caseOne.Config.Backends {
		assert.NotPanics(t, func() {
			leastAlgorithm.healthCheck(&backend, i)
		}, "losing the last live backend must not panic")
	}
	assert.Empty(t, *leastAlgorithm.servers.Load())

	handler := leastAlgorithm.Serve()
	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())

	// Rejoin after the total outage.
	leastAlgorithm.isHostAlive = func(s string) bool {
		return true
	}
	leastAlgorithm.healthCheck(&caseOne.Config.Backends[0], 0)
	assert.Len(t, *leastAlgorithm.servers.Load(), 1)

	ctx = fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	handler(&ctx)
	assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown least-connection calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.Type = "least-connection"
		leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
		assert.NotNil(t, leastAlgorithm)

		// Verify proxy Close() methods are not called yet
		for _, sm := range leastAlgorithm.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := leastAlgorithm.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range leastAlgorithm.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown least-response-time calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.Type = "least-response-time"
		leastResponseTime := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
		assert.NotNil(t, leastResponseTime)

		// Verify proxy Close() methods are not called yet
		for _, sm := range leastResponseTime.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := leastResponseTime.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error for least-response-time algorithm")

		// Verify that Close() was called on all proxy clients
		for _, sm := range leastResponseTime.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown with no servers", func(t *testing.T) {
		emptyCase := mocks.TestCases[3] // Case with 0 servers
		emptyCase.Config.Type = "least-connection"
		emptyLeastAlgorithm := NewLeastAlgorithm(&emptyCase.Config, nil, emptyCase.ProxyFunc)
		if emptyLeastAlgorithm != nil {
			err := emptyLeastAlgorithm.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.Type = "least-connection"
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
		assert.NotNil(t, leastAlgorithm)

		// Give health checker time to start
		time.Sleep(50 * time.Millisecond)

		// Call shutdown - this should stop the health checker goroutine
		err := leastAlgorithm.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range leastAlgorithm.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}

		// Give some time for health checker to actually stop
		time.Sleep(150 * time.Millisecond)
	})
}

func TestStatsWhenBackendDownAtStartup(t *testing.T) {
	cfg := config.Config{
		Type: "least-connection",
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

	balancer := NewLeastAlgorithm(&cfg, nil, mocks.CreateNewMockProxy)
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
		Type: "least-connection",
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

	leastAlgorithm := NewLeastAlgorithm(&cfg, nil, mocks.CreateNewMockProxy).(*LeastAlgorithm)
	assert.Len(t, *leastAlgorithm.servers.Load(), 1)

	leastAlgorithm.isHostAlive = func(string) bool { return true }
	leastAlgorithm.healthCheck(&cfg.Backends[0], 0)

	assert.Len(t, *leastAlgorithm.servers.Load(), 2)
	sm := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte("localhost:8080"+"0"))]
	assert.True(t, sm.isHostAlive)
}

func TestNextConcurrentWithHealthCheck(t *testing.T) {
	hashFunc := func(b []byte) uint32 { return uint32(len(b)) }
	p1 := &mocks.MockProxy{Addr: "localhost:8080"}
	p2 := &mocks.MockProxy{Addr: "localhost:80"}

	var alive atomic.Bool
	leastAlgorithm := &LeastAlgorithm{
		hashFunc:    hashFunc,
		isHostAlive: func(string) bool { return alive.Load() },
		lastIndex:   new(uint32),
		serversMap: map[uint32]*serverMap{
			hashFunc([]byte("localhost:8080" + "0")): {proxy: p1, isHostAlive: true, statsIdx: 0},
			hashFunc([]byte("localhost:80" + "1")):   {proxy: p2, isHostAlive: true, statsIdx: 1},
		},
	}
	servers := []proxy.IProxyClient{p1, p2}
	leastAlgorithm.servers.Store(&servers)

	backend := config.Backend{Url: "localhost:8080"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			alive.Store(false)
			leastAlgorithm.healthCheck(&backend, 0)
			alive.Store(true)
			leastAlgorithm.healthCheck(&backend, 0)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			leastAlgorithm.leastConnectionNext()
			leastAlgorithm.leastResponseTimeNext()
		}
	}
}

func BenchmarkLeastConnectionNext(b *testing.B) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			leastAlgorithm.nextFunc()
		}
	})
}

func BenchmarkLeastResponseTimeNext(b *testing.B) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-response-time"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, nil, caseOne.ProxyFunc).(*LeastAlgorithm)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			leastAlgorithm.nextFunc()
		}
	})
}
