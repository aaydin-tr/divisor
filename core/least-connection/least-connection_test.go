package least_connection

import (
	"strconv"
	"testing"

	"github.com/aaydin-tr/divisor/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewLeastConnection(t *testing.T) {
	for _, l := range mocks.TestCases {
		if l.ExpectedServerCount == 0 {
			leastConnection := NewLeastConnection(&l.Config, l.ProxyFunc)
			assert.Nil(t, leastConnection)
		} else {
			leastConnection := NewLeastConnection(&l.Config, l.ProxyFunc).(*LeastConnection)
			assert.Equal(t, l.ExpectedServerCount, len(leastConnection.serversMap))
			assert.Equal(t, l.ExpectedServerCount, leastConnection.len)
		}
	}
}

func TestNext(t *testing.T) {
	t.Run("with zero pending requests", func(t *testing.T) {
		caseFour := mocks.TestCases[4]
		balancer := NewLeastConnection(&caseFour.Config, caseFour.ProxyFunc)
		assert.NotNil(t, balancer)

		leastConnection := balancer.(*LeastConnection)
		proxy := leastConnection.next()

		assert.IsType(t, &mocks.MockProxy{}, proxy)
		mProxy := proxy.(*mocks.MockProxy)
		assert.Equal(t, caseFour.Config.Backends[0].Url, mProxy.Addr)
	})

	t.Run("with non zero pending requests", func(t *testing.T) {
		caseOne := mocks.TestCases[0]
		balancer := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc)
		assert.NotNil(t, balancer)

		leastConnection := balancer.(*LeastConnection)
		proxy := leastConnection.next()

		assert.IsType(t, &mocks.MockProxy{}, proxy)
		mProxy := proxy.(*mocks.MockProxy)
		assert.Equal(t, caseOne.Config.Backends[1].Url, mProxy.Addr)
	})
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	leastConnection := balancer.(*LeastConnection)
	handlerFunc := leastConnection.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	proxy := leastConnection.next().(*mocks.MockProxy)
	assert.False(t, proxy.IsCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.IsCalled, "expected Server func to be called, but it wasn't")
}

func TestHealthChecker(t *testing.T) {
	caseOne := mocks.TestCases[0]
	leastConnection := &LeastConnection{stopHealthChecker: make(chan bool)}

	leastConnection.isHostAlive = func(s string) bool {
		go func() {
			leastConnection.stopHealthChecker <- true
		}()
		return false
	}
	leastConnection.hashFunc = func(b []byte) uint32 {
		return 0
	}

	caseOne.Config.HealthCheckerTime = 1
	leastConnection.healthChecker(caseOne.Config.Backends)
}

func TestStats(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	leastConnection := balancer.(*LeastConnection)
	stats := leastConnection.Stats()

	for i, backend := range caseOne.Config.Backends {
		hash := leastConnection.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := leastConnection.serversMap[hash]
		p := s.proxy.(*mocks.MockProxy)

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
		assert.Equal(t, backend.Url, p.Addr)
	}
}

func TestRemoveOneServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	leastConnection := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc).(*LeastConnection)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastConnection.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastConnection.serversMap[leastConnection.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastConnection.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := leastConnection.len
		leastConnection.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, leastConnection.len, "expected server to be removed after health check, but it did not.")
	}
}

func TestRemoveAndAddServer(t *testing.T) {
	caseOne := mocks.TestCases[0]
	leastConnection := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc).(*LeastConnection)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastConnection.serversMap))

	// Remove one server
	backend := caseOne.Config.Backends[0]
	if b, ok := leastConnection.serversMap[leastConnection.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		leastConnection.isHostAlive = func(s string) bool {
			return false
		}
		oldServerCount := leastConnection.len
		leastConnection.healthCheck(backend, 0)

		assert.False(t, b.isHostAlive, "expected isHostAlive equal to false, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, oldServerCount, leastConnection.len, "expected server to be removed after health check, but it did not.")
	}

	// Add one server
	if b, ok := leastConnection.serversMap[leastConnection.hashFunc([]byte(backend.Url+strconv.Itoa(0)))]; ok {
		b.isHostAlive = false
		leastConnection.isHostAlive = func(s string) bool {
			return true
		}

		oldServerCount := leastConnection.len
		leastConnection.healthCheck(backend, 0)

		assert.True(t, b.isHostAlive, "expected isHostAlive equal to true, but got %v", b.isHostAlive)
		assert.GreaterOrEqual(t, leastConnection.len, oldServerCount, "expected server to be added after health check, but it did not.")

	}
}

func TestRemmoveAllServers(t *testing.T) {
	caseOne := mocks.TestCases[0]
	leastConnection := NewLeastConnection(&caseOne.Config, caseOne.ProxyFunc).(*LeastConnection)
	assert.Equal(t, caseOne.ExpectedServerCount, len(leastConnection.serversMap))

	// Remove All
	for i, backend := range caseOne.Config.Backends {
		if _, ok := leastConnection.serversMap[leastConnection.hashFunc([]byte(backend.Url+strconv.Itoa(i)))]; ok {
			leastConnection.isHostAlive = func(s string) bool {
				return false
			}

			oldServerCount := leastConnection.len
			if oldServerCount == 1 {
				assert.Panics(t, func() {
					leastConnection.healthCheck(backend, i)
				}, "expected panic after remove all servers")
			} else {
				leastConnection.healthCheck(backend, i)
				assert.GreaterOrEqual(t, oldServerCount, leastConnection.len, "expected server to be removed after health check, but it did not.")
			}
		}
	}
}
