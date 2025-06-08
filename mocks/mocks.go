package mocks

import (
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type MockProxy struct {
	Addr            string
	IsCalled        bool
	pendingRequests int
}

func (m *MockProxy) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	m.IsCalled = true
	return nil
}

func (m *MockProxy) Stat() types.ProxyStat {
	return types.ProxyStat{
		Addr: m.Addr,
	}
}

func (m *MockProxy) PendingRequests() int {
	if m.Addr == "localhost:8080" {
		return 1
	}
	return 0
}

func (m *MockProxy) AvgResponseTime() float64 {
	if m.Addr == "localhost:7070" {
		return 1
	}
	return 0
}

func (m *MockProxy) Close() error {
	return nil
}

func CreateNewMockProxy(b config.Backend, h map[string]string) proxy.IProxyClient {
	return &MockProxy{Addr: b.Url, IsCalled: false}
}

type testCaseStruct struct {
	Config              config.Config
	HealthCheckerFunc   types.IsHostAlive
	HashFunc            types.HashFunc
	ProxyFunc           proxy.ProxyFunc
	ExpectedServerCount int
}

var TestCases = []testCaseStruct{
	{
		Config: config.Config{
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url:    "localhost:8080",
					Weight: 1,
				},
				{
					Url:    "localhost:80",
					Weight: 1,
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
		ExpectedServerCount: 2,
		ProxyFunc:           CreateNewMockProxy,
	},
	{
		Config: config.Config{
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url:    "localhost:8080",
					Weight: 1,
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
		ExpectedServerCount: 1,
		ProxyFunc:           CreateNewMockProxy,
	},
	{
		Config: config.Config{
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url:    "localhost:8080",
					Weight: 1,
				},
				{
					Url:    "localhost:80",
					Weight: 1,
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
		ExpectedServerCount: 0,
		ProxyFunc:           CreateNewMockProxy,
	},
	{
		Config: config.Config{
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
		ExpectedServerCount: 0,
		ProxyFunc:           CreateNewMockProxy,
	},
	{
		Config: config.Config{
			Host: "localhost",
			Port: "8000",
			Backends: []config.Backend{
				{
					Url:    "localhost:7070",
					Weight: 1,
				},
				{
					Url:    "localhost:80",
					Weight: 1,
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
		ExpectedServerCount: 2,
		ProxyFunc:           CreateNewMockProxy,
	},
}
