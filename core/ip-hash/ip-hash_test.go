package ip_hash

import (
	"strconv"
	"testing"

	"github.com/aaydin-tr/balancer/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewIPHash(t *testing.T) {
	for _, ip := range mocks.TestCases {
		if ip.ExpectedServerCount == 0 {
			ipHash := NewIPHash(&ip.Config, ip.ProxyFunc)
			assert.Nil(t, ipHash)
		} else {
			ipHash := NewIPHash(&ip.Config, ip.ProxyFunc).(*IPHash)
			assert.Equal(t, ip.ExpectedServerCount, len(ipHash.serversMap))
		}
	}
}

func TestGet(t *testing.T) {
	caseOne := mocks.TestCases[0]
	balancer := NewIPHash(&caseOne.Config, caseOne.ProxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	proxy := ipHash.get(caseOne.Config.HashFunc([]byte{1, 2, 3}))

	assert.IsType(t, &mocks.MockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := mocks.TestCases[1]
	balancer := NewIPHash(&caseOne.Config, caseOne.ProxyFunc)
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
	balancer := NewIPHash(&caseOne.Config, caseOne.ProxyFunc)
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
	ipHash := NewIPHash(&caseOne.Config, caseOne.ProxyFunc).(*IPHash)
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
	ipHash := NewIPHash(&caseOne.Config, caseOne.ProxyFunc).(*IPHash)
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
	ipHash := NewIPHash(&caseOne.Config, caseOne.ProxyFunc).(*IPHash)
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
