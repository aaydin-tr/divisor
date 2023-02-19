package ip_hash

import (
	"reflect"
	"testing"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/proxy"
	"github.com/valyala/fasthttp"
)

type mockProxy struct{}

func (m *mockProxy) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	return nil
}
func (m *mockProxy) Stat() types.ProxyStat {
	return types.ProxyStat{}
}

func createNewMockProxy(b config.Backend, h map[string]string) proxy.IProxyClient {
	return &mockProxy{}
}

type testCaseStruct struct {
	config              config.Config
	healtCheckerFunc    types.HealtCheckerFunc
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
			HealtCheckerTime: time.Second * 5,
			HealtCheckerFunc: func(string) int {
				return 200
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
				{
					Url: "localhost:80",
				},
			},
			HealtCheckerTime: time.Second * 5,
			HealtCheckerFunc: func(string) int {
				return 500
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
			Type:             "ip-hash",
			Host:             "localhost",
			Port:             "8000",
			Backends:         []config.Backend{},
			HealtCheckerTime: time.Second * 5,
			HealtCheckerFunc: func(s string) int {
				return 500

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
		if len(ip.config.Backends) == 0 {
			ipHash := NewIPHash(&ip.config, ip.proxyFunc)
			if ipHash != nil {
				t.Errorf("expected nil but got %v", ipHash)
			}
		} else {
			ipHash := NewIPHash(&ip.config, ip.proxyFunc).(*IPHash)

			if len(ipHash.serversMap) != ip.expectedServerCount {
				t.Errorf("expected len %v but got %v", ip.expectedServerCount, ipHash.len)
			}
		}
	}
}

func TestGet(t *testing.T) {
	caseOne := testCases[0]
	ipHashI := NewIPHash(&caseOne.config, caseOne.proxyFunc)
	if ipHashI == nil {
		t.Errorf("expected IPHash but got %v", ipHashI)
	}
	ipHash := ipHashI.(*IPHash)

	proxy := ipHash.get(caseOne.config.HashFunc([]byte{1, 2, 3}))

	if reflect.TypeOf(proxy) != reflect.TypeOf(&mockProxy{}) {
		t.Errorf("expected %v but got %v", reflect.TypeOf(&mockProxy{}), reflect.TypeOf(proxy))
	}
}

// TODO
func TestServer(t *testing.T) {
	caseOne := testCases[0]
	ipHashI := NewIPHash(&caseOne.config, caseOne.proxyFunc)
	if ipHashI == nil {
		t.Errorf("expected IPHash but got %v", ipHashI)
	}
	ipHash := ipHashI.(*IPHash)

	handlerFunc := ipHash.Serve()

	ctx := fasthttp.RequestCtx{
		Request: *fasthttp.AcquireRequest(),
	}

	handlerFunc(&ctx)
}
