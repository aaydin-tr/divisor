package proxy

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

func BenchmarkReverseProxyHandlerParallel(b *testing.B) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&backend, nil, nil).(*ProxyClient)

	b.RunParallel(func(pb *testing.PB) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		for pb.Next() {
			p.ReverseProxyHandler(&ctx) //nolint:errcheck
		}
	})
}

// discardAccessLog enables the Access log against io.Discard, so the A/B
// benchmarks measure the emit cost (field building + JSON encoding) without
// terminal I/O.
func discardAccessLog(b *testing.B) {
	b.Helper()
	encoderConfig := zapcore.EncoderConfig{TimeKey: "time", EncodeTime: zapcore.ISO8601TimeEncoder}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(io.Discard), zapcore.InfoLevel)
	b.Cleanup(logger.ReplaceAccessLogger(zap.New(core)))
}

func BenchmarkReverseProxyHandlerAccessLog(b *testing.B) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&backend, nil, nil).(*ProxyClient)
	discardAccessLog(b)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ReverseProxyHandler(&ctx) //nolint:errcheck
	}
}

func BenchmarkReverseProxyHandlerAccessLogParallel(b *testing.B) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&backend, nil, nil).(*ProxyClient)
	discardAccessLog(b)

	b.RunParallel(func(pb *testing.PB) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		for pb.Next() {
			p.ReverseProxyHandler(&ctx) //nolint:errcheck
		}
	})
}

// The cost every request pays while the Access log is off.
func BenchmarkAccessLogDisabledCheck(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if logger.AccessLogEnabled() {
			b.Fatal("access log must be disabled in this benchmark")
		}
	}
}
