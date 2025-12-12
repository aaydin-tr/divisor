package ip_hash

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
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
	ipHash := &IPHash{stopHealthChecker: make(chan bool)}

	ipHash.isHostAlive = func(s string) bool {
		go func() {
			ipHash.stopHealthChecker <- true
		}()
		return false
	}
	ipHash.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	ipHash.healthChecker(caseOne.Config.Backends)
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
		ipHash.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
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
		ipHash.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, ipHash.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := ipHash.serversMap[ipHash.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		ipHash.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := ipHash.len
		ipHash.healthCheck(backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, ipHash.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	ipHash := NewIPHash(&caseOne.Config, nil, caseOne.ProxyFunc).(*IPHash)
	assert.Equal(t, caseOne.ExpectedServerCount, len(ipHash.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := ipHash.serversMap[ipHash.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			ipHash.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := ipHash.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					ipHash.healthCheck(backend, i)
				}, "expected panic after remove all servers")

			} else {
				ipHash.healthCheck(backend, i)
				assert.GreaterOrEqual(t, oldServerCount, ipHash.len, "expected server to be removed after health check, but it did not.")
			}
		}
	}
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
