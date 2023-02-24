package random

import (
	"strconv"
	"testing"
	"time"

	types "github.com/aaydin-tr/balancer/core/types"
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
	return types.ProxyStat{}
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
			Type: "random",
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
			Type: "random",
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
			Type:              "random",
			Host:              "localhost",
			Port:              "8000",
			Backends:          []config.Backend{},
			HealthCheckerTime: time.Second * 5,
			HealthCheckerFunc: func(s string) bool {
				return true

			},
			HashFunc: func(b []byte) uint32 {
				return uint32(len(b))
			},
		},
		expectedServerCount: 0,
		proxyFunc:           createNewMockProxy,
	},
}

func TestNewRandom(t *testing.T) {
	for _, rand := range testCases {
		if rand.expectedServerCount == 0 {
			random := NewRandom(&rand.config, rand.proxyFunc)
			assert.Nil(t, random)
		} else {
			random := NewRandom(&rand.config, rand.proxyFunc).(*Random)
			assert.Equal(t, rand.expectedServerCount, len(random.serversMap))
		}
	}
}

func TestNext(t *testing.T) {
	caseOne := testCases[0]
	balancer := NewRandom(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	proxy := random.next()

	assert.IsType(t, &mockProxy{}, proxy)
}

func TestServer(t *testing.T) {
	caseOne := testCases[1]
	balancer := NewRandom(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	handlerFunc := random.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}
	proxy := random.next().(*mockProxy)
	assert.False(t, proxy.isCalled, "expected Server func not be called, but it was called")
	handlerFunc(&ctx)
	assert.True(t, proxy.isCalled, "expected Server func to be called, but it wasn't")
}

func TestStats(t *testing.T) {
	caseOne := testCases[0]
	balancer := NewRandom(&caseOne.config, caseOne.proxyFunc)
	assert.NotNil(t, balancer)

	random := balancer.(*Random)
	stats := random.Stats()

	for i, backend := range caseOne.config.Backends {
		hash := random.hashFunc([]byte(backend.Url + strconv.Itoa(i)))
		s := random.serversMap[hash]

		assert.Equal(t, s.isHostAlive, stats[i].IsHostAlive)
		assert.Equal(t, hash, stats[i].BackendHash)
	}
}

func TestHealthChecker(t *testing.T) {
	caseOne := testCases[0]
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

	caseOne.config.HealthCheckerTime = 1
	random.healthChecker(caseOne.config.Backends)
}

func TestHealthCheck(t *testing.T) {
	caseOne := testCases[0]
	random := NewRandom(&caseOne.config, caseOne.proxyFunc).(*Random)
	assert.Equal(t, caseOne.expectedServerCount, len(random.serversMap))

	// Remove one server
	backend := caseOne.config.Backends[0]
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

	// Remove All
	for i, backend := range caseOne.config.Backends {
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
