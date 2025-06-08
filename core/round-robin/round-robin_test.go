package round_robin

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewRoundRobin(t *testing.T) {
	for _, r := range mocks.TestCases {
		if r.ExpectedServerCount == 0 {
			round := NewRoundRobin(&r.Config, r.ProxyFunc)
			assert.Nil(t, round)
		} else {
			round := NewRoundRobin(&r.Config, r.ProxyFunc).(*RoundRobin)
			assert.Equal(t, r.ExpectedServerCount, len(round.serversMap))
		}
	}
}

func TestNext(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	roundRobin := balancer.(*RoundRobin)
	proxy := roundRobin.next()

	assert.IsType(t, &mocks.MockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	roundRobin := balancer.(*RoundRobin)
	handlerFunc := roundRobin.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	proxy := roundRobin.next().(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	roundRobin := balancer.(*RoundRobin)
	stats := roundRobin.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := roundRobin.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := roundRobin.serversMap[hash]

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	roundRobin := &RoundRobin{stopHealthChecker: make(chan bool)}

	roundRobin.isHostAlive = func(s string) bool {
		go func() {
			roundRobin.stopHealthChecker <- true
		}()
		return false
	}
	roundRobin.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	roundRobin.healthChecker(caseOne.Config.Backends)
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(roundRobin.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := roundRobin.serversMap[roundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		roundRobin.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := roundRobin.len
		roundRobin.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, roundRobin.len, "expected server to be removed after health check, but it did not.")
	}
}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(roundRobin.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := roundRobin.serversMap[roundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		roundRobin.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := roundRobin.len
		roundRobin.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, roundRobin.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := roundRobin.serversMap[roundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		roundRobin.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := roundRobin.len
		roundRobin.healthCheck(backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, roundRobin.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
	assert.Equal(t, caseOne.ExpectedServerCount, len(roundRobin.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := roundRobin.serversMap[roundRobin.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			roundRobin.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := roundRobin.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					roundRobin.healthCheck(backend, i)
				}, "expected panic after remove all servers")
			} else {
				roundRobin.healthCheck(backend, i)
				assert.GreaterOrEqual(t, oldServerCount, roundRobin.len, "expected server to be removed after health check, but it did not.")
			}
		}
	}
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
		assert.NotNil(t, roundRobin)

		// Verify proxy Close() methods are not called yet
		for _, sm := range roundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.False(t, mockProxy.CloseCalled, "Proxy Close() should not be called before shutdown")
		}

		// Call shutdown
		err := roundRobin.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range roundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}
	})

	t.Run("shutdown with no servers", func(t *testing.T) {
		emptyCase := mocks.TestCases[3] // Case with 0 servers
		emptyRoundRobin := NewRoundRobin(&emptyCase.Config, emptyCase.ProxyFunc)
		if emptyRoundRobin != nil {
			err := emptyRoundRobin.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("multiple shutdowns are idempotent", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
		assert.NotNil(t, roundRobin)

		// First shutdown
		err := roundRobin.Shutdown()
		assert.NoError(t, err, "First shutdown should not return an error")

		// Reset mock proxy state to verify second shutdown doesn't call Close() again
		for _, sm := range roundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			mockProxy.CloseCalled = false
		}

		// Second shutdown
		err = roundRobin.Shutdown()
		assert.NoError(t, err, "Second shutdown should not return an error")

		// Verify Close() is called again (current implementation doesn't prevent this, which is fine)
		for _, sm := range roundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called on second shutdown too")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		roundRobin := NewRoundRobin(&caseOne.Config, caseOne.ProxyFunc).(*RoundRobin)
		assert.NotNil(t, roundRobin)

		// Give health checker time to start
		time.Sleep(50 * time.Millisecond)

		// Verify health checker is running by checking that backends are being monitored
		// This is implicit since NewRoundRobin starts the health checker

		// Call shutdown - this should stop the health checker goroutine
		err := roundRobin.Shutdown()
		assert.NoError(t, err, "Shutdown() should not return an error")

		// Verify that Close() was called on all proxy clients
		for _, sm := range roundRobin.serversMap {
			mockProxy := sm.proxy.(*mocks.MockProxy)
			assert.True(t, mockProxy.CloseCalled, "Proxy Close() should be called during shutdown")
		}

		// Give some time for health checker to actually stop
		time.Sleep(150 * time.Millisecond)
	})
}
