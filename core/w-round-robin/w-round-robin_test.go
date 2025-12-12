package w_round_robin

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
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

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
		assert.Equal(t, backend.Url, p.Addr)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := &WRoundRobin{stopHealthChecker: make(chan bool)}

	wRoundRobin.isHostAlive = func(s string) bool {
		go func() {
			wRoundRobin.stopHealthChecker <- true
		}()
		return false
	}
	wRoundRobin.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	wRoundRobin.healthChecker(caseOne.Config.Backends)
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
		oldServerCount := wRoundRobin.len
		wRoundRobin.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, wRoundRobin.len, "expected server to be removed after health check, but it did not.")
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
		oldServerCount := wRoundRobin.len
		wRoundRobin.healthCheck(&backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, wRoundRobin.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		wRoundRobin.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := wRoundRobin.len
		wRoundRobin.healthCheck(&backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, wRoundRobin.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	wRoundRobin := NewWRoundRobin(&caseOne.Config, nil, caseOne.ProxyFunc).(*WRoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(wRoundRobin.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := wRoundRobin.serversMap[wRoundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			wRoundRobin.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := wRoundRobin.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					wRoundRobin.healthCheck(&backend, i)
				}, "expected panic after remove all servers")
			} else {
				wRoundRobin.healthCheck(&backend, i)
				assert.GreaterOrEqual(t, oldServerCount, wRoundRobin.len, "expected server to be removed after health check, but it did not.")
			}
		}
	}
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
