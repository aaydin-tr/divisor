package random

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
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
		oldServerCount := random.len
		random.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, random.len, "expected server to be removed after health check, but it did not.")
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
		oldServerCount := random.len
		random.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, random.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := random.serversMap[random.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		random.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := random.len
		random.healthCheck(backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, random.len, oldServerCount, "expected server to be added after health check, but it did not.")

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

			oldServerCount := random.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					random.healthCheck(backend, i)
				}, "expected panic after remove all servers")

			} else {
				random.healthCheck(backend, i)
				assert.GreaterOrEqual(t, oldServerCount, random.len, "expected server to be removed after health check, but it did not.")
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
