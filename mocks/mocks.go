package mocks

import (
	"fmt"
	"net"
	"sync"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/valyala/fasthttp"
)

// MockProxy satisfies proxy.IProxyClient with values a test sets directly:
// Pending for least-connection, ResTime (ms) for least-response-time.
type MockProxy struct {
	Addr               string
	ResTime            float64
	Pending            int
	IsCalled           bool
	CloseCalled        bool
	middlewareExecutor *middleware.Executor
	// Guards ResTime in the methods: the Probe loop resets it while
	// selection reads it, and the real ProxyClient is atomic there.
	resTimeMu sync.Mutex
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
	return m.Pending
}

func (m *MockProxy) AvgResponseTime() float64 {
	return 0
}

func (m *MockProxy) RecentResponseTime() float64 {
	m.resTimeMu.Lock()
	resTime := m.ResTime
	m.resTimeMu.Unlock()
	return resTime
}

func (m *MockProxy) ResetRecentResponseTime() {
	m.resTimeMu.Lock()
	m.ResTime = 0
	m.resTimeMu.Unlock()
}

func (m *MockProxy) Close() error {
	m.CloseCalled = true
	return nil
}

func CreateNewMockProxy(b *config.Backend, h map[string]string, middlewareExecutor *middleware.Executor) proxy.IProxyClient {
	return &MockProxy{Addr: b.Url, IsCalled: false, middlewareExecutor: middlewareExecutor}
}

// MockBalancer satisfies types.IBalancer with a no-op handler, for tests that
// need a balancer to hand to the server or monitoring layers.
type MockBalancer struct{}

func (m *MockBalancer) Serve() func(ctx *fasthttp.RequestCtx) { return func(*fasthttp.RequestCtx) {} }
func (m *MockBalancer) Stats() []types.ProxyStat              { return nil }
func (m *MockBalancer) Shutdown() error                       { return nil }

const firstBackendPort = 8080

// Backends builds n Backends on localhost:8080.. with a MockProxy each.
func NewBackends(n int) []*pool.Backend {
	backends := make([]*pool.Backend, n)
	for i := range backends {
		addr := fmt.Sprintf("localhost:%d", firstBackendPort+i)
		backends[i] = &pool.Backend{Index: i, Addr: addr, Weight: 1, ProbeURL: "http://" + addr + "/", Proxy: &MockProxy{Addr: addr}}
	}
	return backends
}

func MockProxyOf(b *pool.Backend) *MockProxy { return b.Proxy.(*MockProxy) }

func JoinAll(b pool.Balancer, backends []*pool.Backend) {
	for _, backend := range backends {
		b.Join(backend)
	}
}

// RequestFrom is a request whose client IP is ip, for ip-hash and Serve tests.
func RequestFrom(ip string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}, nil)
	return ctx
}

// RecordingBalancer is the fake on the Pool → Balancer seam: it records every
// transition and picks the first Backend it was handed.
type RecordingBalancer struct {
	mu            sync.Mutex
	joins         []*pool.Backend
	leaves        []*pool.Backend
	aliveBackends []*pool.Backend
}

func (r *RecordingBalancer) Join(b *pool.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joins = append(r.joins, b)
	r.aliveBackends = append(r.aliveBackends, b)
}

func (r *RecordingBalancer) Leave(b *pool.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaves = append(r.leaves, b)
	kept := r.aliveBackends[:0]
	for _, a := range r.aliveBackends {
		if a != b {
			kept = append(kept, a)
		}
	}
	r.aliveBackends = kept
}

func (r *RecordingBalancer) Pick(*fasthttp.RequestCtx) *pool.Backend {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.aliveBackends) == 0 {
		return nil
	}
	return r.aliveBackends[0]
}

// Snapshot returns copies of the Join and Leave calls seen so far.
func (r *RecordingBalancer) Transitions() (joins, leaves []*pool.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*pool.Backend(nil), r.joins...), append([]*pool.Backend(nil), r.leaves...)
}

// ProbeTable answers Probes per Backend; Backends it was not told about are
// Alive.
type ProbeTable struct {
	mu   sync.Mutex
	down map[string]bool
}

func (p *ProbeTable) SetAlive(b *pool.Backend, alive bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down == nil {
		p.down = map[string]bool{}
	}
	p.down[b.ProbeURL] = !alive
}

func (p *ProbeTable) Probe(url string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.down[url]
}
