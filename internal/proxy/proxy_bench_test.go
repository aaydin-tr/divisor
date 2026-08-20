package proxy

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

func BenchmarkRecordResponseTime(b *testing.B) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.recordResponseTime(time.Millisecond)
	}
}

func BenchmarkRecordResponseTimeParallel(b *testing.B) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.recordResponseTime(time.Millisecond)
		}
	})
}

func BenchmarkRecentResponseTimeRead(b *testing.B) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)
	p.recordResponseTime(time.Millisecond)
	var sink float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = p.RecentResponseTime()
	}
	_ = sink
}

func BenchmarkReverseProxyHandler(b *testing.B) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&backend, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ReverseProxyHandler(&ctx) //nolint:errcheck
	}
}
