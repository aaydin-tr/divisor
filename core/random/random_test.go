package random

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

func TestNewRandom(t *testing.T) {
	for _, rand := range mocks.TestCases {
		if rand.ExpectedServerCount == 0 {
			random := NewRandom(&rand.Config, nil, rand.ProxyFunc)
			assert.Nil(t, random)
		} else {
			random := NewRandom(&rand.Config, nil, rand.ProxyFunc).(*Random)
			assert.Equal(t, rand.ExpectedServerCount, len(random.serversMap))
		}
	}
}

func TestNext(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	proxy := random.next()

	assert.IsType(t, &mocks.MockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	handlerFunc := random.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	proxy := random.next().(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	stats := random.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := random.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := random.serversMap[hash]

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	random := &Random{stopHealthChecker: make(chan bool)}

	random.isHostAlive = func(s string) bool {
		go func() {
			random.stopHealthChecker <- true
		}()
		return false
	}
	random.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	random.healthChecker(caseOne.Config.Backends)
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	random := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc).(*Random)
	assert.Equal(t, caseOne.ExpectedServerCount, len(random.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := random.serversMap[random.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		random.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*random.servers.Load())
		random.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, len(*random.servers.Load()), "expected server to be removed after health check, but it did not.")
	}

}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	random := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc).(*Random)
	assert.Equal(t, caseOne.ExpectedServerCount, len(random.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := random.serversMap[random.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		random.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := len(*random.servers.Load())
		random.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, len(*random.servers.Load()), "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := random.serversMap[random.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		random.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := len(*random.servers.Load())
		random.healthCheck(&backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, len(*random.servers.Load()), oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	random := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc).(*Random)
	assert.Equal(t, caseOne.ExpectedServerCount, len(random.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := random.serversMap[random.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			random.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := len(*random.servers.Load())
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					random.healthCheck(&backend, i)
				}, "expected panic after remove all servers")

			} else {
				random.healthCheck(&backend, i)
				assert.GreaterOrEqual(t, oldServerCount, len(*random.servers.Load()), "expected server to be removed after health check, but it did not.")
			}
		}
	}
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		random := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc).(*Random)
		assert.NotNil(t, random)

		// Verify proxy Close() methods are not called yet
		for _, sm := range random.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := random.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range random.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown with no servers", func(t *testing.T) {
		emptyCase := mocks.TestCases[3] // Case with 0 servers
		emptyRandom := NewRandom(&emptyCase.Config, nil, emptyCase.ProxyFunc)
		if emptyRandom != nil {
			err := emptyRandom.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		random := NewRandom(&caseOne.Config, nil, caseOne.ProxyFunc).(*Random)
		assert.NotNil(t, random)

		// Give health checker time to start
		time.Sleep(50 * time.Millisecond)

		// Call shutdown - this should stop the health checker goroutine
		err := random.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range random.serversMap {
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

	balancer := NewRandom(&cfg, nil, mocks.CreateNewMockProxy)
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

	random := NewRandom(&cfg, nil, mocks.CreateNewMockProxy).(*Random)
	assert.Len(t, *random.servers.Load(), 1)

	random.isHostAlive = func(string) bool { return true }
	random.healthCheck(&cfg.Backends[0], 0)

	assert.Len(t, *random.servers.Load(), 2)
	sm := random.serversMap[random.hashFunc([]byte("localhost:8080"+"0"))]
	assert.True(t, sm.isHostAlive)
}

func TestNextConcurrentWithHealthCheck(t *testing.T) {
	hashFunc := func(b []byte) uint32 { return uint32(len(b)) }
	p1 := &mocks.MockProxy{Addr: "localhost:8080"}
	p2 := &mocks.MockProxy{Addr: "localhost:80"}

	var alive atomic.Bool
	random := &Random{
		hashFunc:    hashFunc,
		isHostAlive: func(string) bool { return alive.Load() },
		serversMap: map[uint32]*serverMap{
			hashFunc([]byte("localhost:8080" + "0")): {proxy: p1, isHostAlive: true, statsIdx: 0},
			hashFunc([]byte("localhost:80" + "1")):   {proxy: p2, isHostAlive: true, statsIdx: 1},
		},
	}
	servers := []proxy.IProxyClient{p1, p2}
	random.servers.Store(&servers)

	backend := config.Backend{Url: "localhost:8080"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			alive.Store(false)
			random.healthCheck(&backend, 0)
			alive.Store(true)
			random.healthCheck(&backend, 0)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			random.next()
		}
	}
}
