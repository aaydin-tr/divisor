package least_response_time

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// BenchmarkPick times Pick over real ProxyClients (MockProxy's Trial request
// claim is not the real one), serial and parallel, as the pool grows.
func BenchmarkPick(b *testing.B) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(2 * time.Millisecond) // a coarse clock (Windows) still scores it above 0
	}))
	defer backendServer.Close()
	backendAddress := backendServer.Listener.Addr().String()

	for _, scenario := range []struct {
		name            string
		poolSize        int
		unmeasuredCount int
	}{
		{"backends=2/all-measured", 2, 0},
		{"backends=4/all-measured", 4, 0},
		{"backends=16/all-measured", 16, 0},
		{"backends=64/all-measured", 64, 0},
		{"backends=4/one-unmeasured", 4, 1},
		{"backends=16/one-unmeasured", 16, 1},
		{"backends=64/one-unmeasured", 64, 1},
	} {
		lrt := poolOfProxyClients(b, backendAddress, scenario.poolSize, scenario.unmeasuredCount)
		b.Run(scenario.name+"/serial", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				lrt.Pick(nil)
			}
		})
		b.Run(scenario.name+"/parallel", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					lrt.Pick(nil)
				}
			})
		})
	}
}

// poolOfProxyClients scores all but the last unmeasuredCount Backends with one
// request each; the unmeasured go last, the old strict-minimum scan's worst case.
func poolOfProxyClients(b *testing.B, backendAddress string, poolSize, unmeasuredCount int) pool.Balancer {
	lrt := New(nil)
	for i := range poolSize {
		proxyClient := proxy.NewProxyClient(&config.Backend{Url: backendAddress}, nil, nil)
		b.Cleanup(func() { proxyClient.Close() }) //nolint:errcheck
		lrt.Join(&pool.Backend{Index: i, Addr: backendAddress, Weight: 1, Proxy: proxyClient})
		if i < poolSize-unmeasuredCount {
			scoreWithOneRequest(b, proxyClient)
		}
	}
	return lrt
}

func scoreWithOneRequest(b *testing.B, proxyClient proxy.IProxyClient) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 40000}, nil)
	if err := proxyClient.ReverseProxyHandler(ctx); err != nil || proxyClient.RecentResponseTime() == 0 {
		b.Fatalf("the scoring request left the Backend unmeasured: %v", err)
	}
}
