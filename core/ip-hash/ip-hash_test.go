package ip_hash

import (
	"strconv"
	"testing"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type mockProxy struct {
	addr     string
	isCalled bool
}

func (m *mockProxy) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	m.isCalled = true
	return nil
}

func (m *mockProxy) Stat() types.ProxyStat {
	return types.ProxyStat{
		Addr: m.addr,
	}
}

func createNewMockProxy(b config.Backend, h map[string]string) proxy.IProxyClient {
	return &mockProxy{addr: b.Url, isCalled: false}
}

type testCaseStruct struct {
	config              config.Config
	healtCheckerFunc    types.IsHostAlive
	hashFunc            types.HashFunc
	proxyFunc           proxy.ProxyFunc
	expectedServerCount int
}

var testCases = []testCaseStruct{
	{
		config: config.Config{
			Type: "ip-hash",
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url: "localhost:8080",
				},
				{
					Url: "localhost:80",
				},
			},
			HealthCheckerTime: time.Second * 5,
			HealthCheckerFunc: func(string) bool {
				return true
			},
			HashFunc: func(b []byte) uint32 {
				return uint32(len(b))
			},
		},
		expectedServerCount: 2,
		proxyFunc:           createNewMockProxy,
	},
	{
		config: config.Config{
			Type: "ip-hash",
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url: "localhost:8080",
				},
			},
			HealthCheckerTime: time.Second * 5,
			HealthCheckerFunc: func(string) bool {
				return true
			},
			HashFunc: func(b []byte) uint32 {
				return uint32(len(b))
			},
		},
		expectedServerCount: 1,
		proxyFunc:           createNewMockProxy,
	},
	{
		config: config.Config{
			Type: "ip-hash",
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url: "localhost:8080",
				},
				{
					Url: "localhost:80",
				},
			},
			HealthCheckerTime: time.Second * 5,
			HealthCheckerFunc: func(string) bool {
				return false
			},
			HashFunc: func(b []byte) uint32 {
				return uint32(len(b))
			},
		},
		expectedServerCount: 0,
		proxyFunc:           createNewMockProxy,
	},
	{
		config: config.Config{
			Type:              "ip-hash",
			Host:              "localhost",
			Port:              "8000",
			Backends:          []config.Backend{},
			HealthCheckerTime: time.Second * 5,
			HealthCheckerFunc: func(s string) bool {
				return false

			},
			HashFunc: func(b []byte) uint32 {
				return uint32(len(b))
			},
		},
		expectedServerCount: 0,
		proxyFunc:           createNewMockProxy,
	},
}

func TestNewIPHash(t *testing.T) {
	for _, ip := range testCases {
		if ip.expectedServerCount == 0 {
			ipHash := NewIPHash(&ip.config, ip.proxyFunc)
			assert.Nil(t, ipHash)
		} else {
			ipHash := NewIPHash(&ip.config, ip.proxyFunc).(*IPHash)
			assert.Equal(t, ip.expectedServerCount, len(ipHash.serversMap))
		}
	}
}

func TestGet(t *testing.T) {
	caseOne := testCases[0]
	balancer := NewIPHash(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	proxy := ipHash.get(caseOne.config.HashFunc([]byte{1, 2, 3}))

	assert.IsType(t, &mockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := testCases[1]
	balancer := NewIPHash(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	handlerFunc := ipHash.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	proxy := ipHash.get(caseOne.config.HashFunc([]byte{1})).(*mockProxy)
	assert.False(t, proxy.isCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.isCalled, "expected Server func to be called, but it wasn't")
}

func TestHealthCheck(t *testing.T) {
	caseOne := testCases[0]
	ipHash := NewIPHash(&caseOne.config, caseOne.proxyFunc).(*IPHash)
	assert.Equal(t, caseOne.expectedServerCount, len(ipHash.serversMap))

	// Remove one server
	backend := caseOne.config.Backends[0]
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

	// Remove All
	for i, backend := range caseOne.config.Backends {
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

func TestStats(t *testing.T) {
	caseOne := testCases[0]
	balancer := NewIPHash(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	ipHash := balancer.(*IPHash)
	stats := ipHash.Stats()

	for i, backend := range caseOne.config.Backends {
		hash := ipHash.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := ipHash.serversMap[hash]

		assert.Equal(t, s.node.Addr, stats[i].Addr)
		assert.Equal(t, hash, stats[i].BackendHash)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := testCases[0]
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

	caseOne.config.HealthCheckerTime = 1
	ipHash.healthChecker(caseOne.config.Backends)
}
