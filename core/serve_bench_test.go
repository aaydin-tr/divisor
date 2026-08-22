package core

import (
	"net"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

func benchConfig(t string) *config.Config {
	cfg := &config.Config{Type: t, HealthCheckerTime: time.Hour, HashFunc: helper.HashFunc,
		HealthCheckerFunc: func(string) bool { return true }}
	for _, a := range []string{"localhost:8080", "localhost:8081", "localhost:8082"} {
		cfg.Backends = append(cfg.Backends, config.Backend{Url: a, Weight: 2})
	}
	return cfg
}

func benchCtx(ip string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000}, nil)
	return ctx
}

// BenchmarkServe times the selection path alone (MockProxy, no network):
// one Pick plus the handler dispatch, per balancer, serial and parallel.
func BenchmarkServe(b *testing.B) {
	for _, t := range config.ValidTypes {
		lb := NewBalancer(benchConfig(t), nil, mocks.CreateNewMockProxy)
		if lb == nil {
			b.Fatal("nil balancer for " + t)
		}
		h := lb.Serve()
		b.Run(t+"/serial", func(b *testing.B) {
			ctx := benchCtx("10.1.2.3")
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				h(ctx)
			}
		})
		b.Run(t+"/parallel", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				ctx := benchCtx("10.1.2.4")
				for pb.Next() {
					h(ctx)
				}
			})
		})
		lb.Shutdown() //nolint:errcheck
	}
}
