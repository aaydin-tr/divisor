package proxy

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type mockServer struct {
	done  chan struct{}
	ready chan struct{}
}

func (m *mockServer) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if _, ok := req.Header["Wait"]; ok {
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := req.Header["After"]; ok {
		for _, h := range hopHeaders {
			if string(h) != "Trailer" {
				res.Header().Add(string(h), string(h))
			}
		}
	}
	if _, ok := req.Header["Pending"]; ok {
		m.ready <- struct{}{}
		<-m.done
	}

	res.WriteHeader(200)
}

var backend = config.Backend{
	Url:    "localhost:8080",
	Weight: 1,
}

var protocolRegex = regexp.MustCompile(`(^https?://)`)

func TestNewProxyClient(t *testing.T) {

	customHeaders := make(map[string]string)
	p := NewProxyClient(backend, customHeaders)
	assert.IsType(t, &ProxyClient{}, p)
	assert.Equal(t, backend.Url, p.(*ProxyClient).Addr)
}

func TestStat(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(backend, customHeaders).(*ProxyClient)

	t.Run("with zero request", func(t *testing.T) {
		stat := p.Stat()
		assert.Equal(t, float64(0), stat.AvgResTime)
		assert.Equal(t, uint64(0), stat.TotalReqCount)
		assert.Equal(t, backend.Url, stat.Addr)
		assert.Equal(t, 0, stat.ConnsCount)
	})

	t.Run("with one request", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		p.ReverseProxyHandler(&ctx)

		stat := p.Stat()
		assert.Equal(t, uint64(1), stat.TotalReqCount)
		assert.Equal(t, backend.Url, stat.Addr)
		assert.Equal(t, 1, stat.ConnsCount)

	})

	t.Run("with more request", func(t *testing.T) {
		for i := 0; i < 9; i++ {
			ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
			ctx.Request.Header.Add("Wait", "true")
			p.ReverseProxyHandler(&ctx)
		}

		stat := p.Stat()

		assert.Equal(t, uint64(10), stat.TotalReqCount)
		assert.Equal(t, backend.Url, stat.Addr)
		assert.Equal(t, 1, stat.ConnsCount)
		assert.Greater(t, stat.AvgResTime, float64(0))
	})
}

func TestReverseProxyHandler(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(backend, customHeaders).(*ProxyClient)

	t.Run("should update totalRequestCount", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		p.ReverseProxyHandler(&ctx)
		assert.Equal(t, uint64(1), atomic.LoadUint64(p.totalRequestCount))
	})

	t.Run("should remove hop header before request", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		for _, h := range hopHeaders {
			ctx.Request.Header.AddBytesKV(h, h)
		}

		p.ReverseProxyHandler(&ctx)
		ctx.Request.Header.VisitAll(func(key, value []byte) {
			assert.NotContains(t, hopHeaders, key)
		})
	})

	t.Run("should remove hop header after request", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.Header.Add("After", "true")

		p.ReverseProxyHandler(&ctx)
		ctx.Response.Header.VisitAll(func(key, value []byte) {
			assert.NotContains(t, hopHeaders, key)
		})
	})

	t.Run("x-forwarded-for and host header should be added", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		p.ReverseProxyHandler(&ctx)
		assert.Equal(t, "0.0.0.0", string(ctx.Request.Header.PeekBytes(XForwardedFor)))
		assert.Equal(t, backend.Url, string(ctx.Request.Header.Peek("Host")))

	})

	t.Run("with error", func(t *testing.T) {
		pErr := NewProxyClient(config.Backend{}, customHeaders).(*ProxyClient)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.SetHost("test")
		err := pErr.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Contains(t, string(ctx.Response.Body()), err.Error())
		assert.Equal(t, ctx.Response.StatusCode(), fasthttp.StatusInternalServerError)
	})

	t.Run("set custom headers", func(t *testing.T) {
		customHeaders["X-Remote-Addr"] = "$remote_addr"
		customHeaders["X-Time"] = "$time"
		customHeaders["X-Incremental"] = "$incremental"
		customHeaders["X-Uuid"] = "$uuid"

		pHeader := NewProxyClient(backend, customHeaders).(*ProxyClient)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		pHeader.ReverseProxyHandler(&ctx)

		_, err := uuid.Parse(string(ctx.Request.Header.Peek("X-Uuid")))
		assert.Nil(t, err)
		assert.Equal(t, ctx.RemoteIP().String(), string(ctx.Request.Header.Peek("X-Remote-Addr")))
		assert.Equal(t, "1", string(ctx.Request.Header.Peek("X-Incremental")))
		assert.GreaterOrEqual(t, time.Now().Local().Format("2006-01-02T15:04:05.000Z"), string(ctx.Request.Header.Peek("X-Time")))
	})
}

func TestPendingRequests(t *testing.T) {

	customHeaders := make(map[string]string)
	handler := mockServer{done: make(chan struct{}), ready: make(chan struct{})}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(backend, customHeaders).(*ProxyClient)

	assert.Equal(t, 0, p.PendingRequests())
	concurrency := 10
	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	ctx.Request.Header.Add("Pending", "true")
	for i := 0; i < 10; i++ {
		go p.ReverseProxyHandler(&ctx)
	}

	for i := 0; i < concurrency; i++ {
		select {
		case <-handler.ready:
		}
	}

	close(handler.done)
	assert.Equal(t, concurrency, p.PendingRequests())
}
