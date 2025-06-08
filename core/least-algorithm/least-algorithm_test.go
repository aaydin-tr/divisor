package least_algorithm

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
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

			leastAlgorithm := NewLeastAlgorithm(&testConfig, l.ProxyFunc)
			assert.Nil(t, leastAlgorithm)
		} else {
			testConfig := l.Config
			testConfig.Type = "least-connection"
			if i%2 == 0 {
				testConfig.Type = "least-response-time"
			}

			leastAlgorithm := NewLeastAlgorithm(&testConfig, l.ProxyFunc).(*LeastAlgorithm)
			assert.Equal(t, l.ExpectedServerCount, len(leastAlgorithm.serversMap))
			assert.Equal(t, l.ExpectedServerCount, leastAlgorithm.len)
		}
	}
}

func TestNewLeastAlgorithmWithoutAlgorithmType(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = ""
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc)
	assert.Nil(t, leastAlgorithm)
}

func TestNext(t *testing.T) {
	t.Run("least-connection", func(t *testing.T) {
		t.Run("with zero pending requests", func(t *testing.T) {
			caseFour := mocks.TestCases[4]
			caseFour.Config.Type = "least-connection"
			balancer := NewLeastAlgorithm(&caseFour.Config, caseFour.ProxyFunc)
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
			balancer := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc)
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
			balancer := NewLeastAlgorithm(&caseFive.Config, caseFive.ProxyFunc)
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
			balancer := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc)
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
	balancer := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc)
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
	balancer := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc)
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
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastAlgorithm.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := leastAlgorithm.len
		leastAlgorithm.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, leastAlgorithm.len, "expected server to be removed after health check, but it did not.")
	}
}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastAlgorithm.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := leastAlgorithm.len
		leastAlgorithm.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, leastAlgorithm.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		leastAlgorithm.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := leastAlgorithm.len
		leastAlgorithm.healthCheck(backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, leastAlgorithm.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	caseOne.Config.Type = "least-connection"
	leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastAlgorithm.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := leastAlgorithm.serversMap[leastAlgorithm.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			leastAlgorithm.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := leastAlgorithm.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					leastAlgorithm.healthCheck(backend, i)
				}, "expected panic after remove all servers")
			} else {
				leastAlgorithm.healthCheck(backend, i)
				assert.GreaterOrEqual(t, oldServerCount, leastAlgorithm.len, "expected server to be removed after health check, but it did not.")
			}
		}
	}
}

func TestShutdown(t *testing.T) {
	t.Run("shutdown least-connection calls close on all proxies", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.Type = "least-connection"
		leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
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
		leastResponseTime := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
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
		emptyLeastAlgorithm := NewLeastAlgorithm(&emptyCase.Config, emptyCase.ProxyFunc)
		if emptyLeastAlgorithm != nil {
			err := emptyLeastAlgorithm.Shutdown()
			assert.NoError(t, err, "Shutdown() should not return an error even with no servers")
		}
	})

	t.Run("shutdown with actual health checker goroutine", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		caseOne.Config.Type = "least-connection"
		caseOne.Config.HealthCheckerTime = 100 * time.Millisecond // Fast health check for testing
		leastAlgorithm := NewLeastAlgorithm(&caseOne.Config, caseOne.ProxyFunc).(*LeastAlgorithm)
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
