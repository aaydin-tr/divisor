package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	middlewarePkg "github.com/aaydin-tr/divisor/pkg/middleware"
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
	if _, ok := req.Header["Hang"]; ok {
		time.Sleep(300 * time.Millisecond)
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
	if _, ok := req.Header["Stamp"]; ok {
		res.Header().Set("X-Backend-Stamp", "1")
	}

	res.WriteHeader(200)
}

// mockMiddleware is a test middleware implementation
type mockMiddleware struct {
	onRequestFunc  func(ctx *middleware.Context) error
	onResponseFunc func(ctx *middleware.Context, err error) error
	mu             sync.Mutex
	requestCalls   int
	responseCalls  int
}

func (m *mockMiddleware) OnRequest(ctx *middleware.Context) error {
	m.mu.Lock()
	m.requestCalls++
	m.mu.Unlock()
	if m.onRequestFunc != nil {
		return m.onRequestFunc(ctx)
	}
	return nil
}

func (m *mockMiddleware) OnResponse(ctx *middleware.Context, err error) error {
	m.mu.Lock()
	m.responseCalls++
	m.mu.Unlock()
	if m.onResponseFunc != nil {
		return m.onResponseFunc(ctx, err)
	}
	return nil
}

func (m *mockMiddleware) getRequestCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestCalls
}

func (m *mockMiddleware) getResponseCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.responseCalls
}

// createTestProxyWithMiddlewares creates a ProxyClient with test middlewares injected
// This helper directly creates a ProxyClient and injects the test executor
func createTestProxyWithMiddlewares(backend config.Backend, customHeaders map[string]string, middlewares ...middleware.Middleware) *ProxyClient {
	testExec := createTestExecutor(middlewares)
	return NewProxyClient(&backend, customHeaders, testExec).(*ProxyClient)
}

// createTestExecutor creates an executor with the given middlewares for testing
// We use reflection and unsafe to bypass the private field restriction
func createTestExecutor(middlewares []middleware.Middleware) *middlewarePkg.Executor {
	executor := &middlewarePkg.Executor{}

	// Use reflection to access the unexported 'middlewares' field
	v := reflect.ValueOf(executor).Elem()
	middlewaresField := v.FieldByName("middlewares")

	// Use unsafe to make the field settable
	middlewaresField = reflect.NewAt(middlewaresField.Type(), unsafe.Pointer(middlewaresField.UnsafeAddr())).Elem()
	middlewaresField.Set(reflect.ValueOf(middlewares))

	return executor
}

var backend = config.Backend{
	Url:    "localhost:8080",
	Weight: 1,
}

var protocolRegex = regexp.MustCompile(`(^https?://)`)

func TestNewProxyClient(t *testing.T) {

	customHeaders := make(map[string]string)
	p := NewProxyClient(&backend, customHeaders, nil)
	assert.IsType(t, &ProxyClient{}, p)
	assert.Equal(t, backend.Url, p.(*ProxyClient).Addr)
}

func TestStat(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)

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

func TestProxyTimeoutReturns504(t *testing.T) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()

	b := config.Backend{
		Url:          protocolRegex.ReplaceAllString(bServer.URL, ""),
		ProxyTimeout: 50 * time.Millisecond,
	}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	ctx.Request.Header.Add("Hang", "true")

	err := p.ReverseProxyHandler(&ctx)
	assert.ErrorIs(t, err, fasthttp.ErrTimeout)
	assert.Equal(t, fasthttp.StatusGatewayTimeout, ctx.Response.StatusCode())
}

func TestServerErrorStatusMapping(t *testing.T) {
	p := &ProxyClient{}

	dialTimeout := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	res := fasthttp.AcquireResponse()
	p.serverError(res, dialTimeout)
	assert.Equal(t, fasthttp.StatusBadGateway, res.StatusCode())
	assert.JSONEq(t, `{"message":"bad gateway"}`, string(res.Body()))

	res = fasthttp.AcquireResponse()
	p.serverError(res, fasthttp.ErrTimeout)
	assert.Equal(t, fasthttp.StatusGatewayTimeout, res.StatusCode())
	assert.JSONEq(t, `{"message":"gateway timeout"}`, string(res.Body()))
}

func TestReverseProxyHandler(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)

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
		pErr := NewProxyClient(&config.Backend{}, customHeaders, nil).(*ProxyClient)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.SetHost("test")
		err := pErr.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, fasthttp.StatusBadGateway, ctx.Response.StatusCode())
		assert.JSONEq(t, `{"message":"bad gateway"}`, string(ctx.Response.Body()))
		assert.NotContains(t, string(ctx.Response.Body()), err.Error(), "dial detail must stay in the log")
	})

	t.Run("set custom headers", func(t *testing.T) {
		customHeaders["X-Remote-Addr"] = "$remote_addr"
		customHeaders["X-Time"] = "$time"
		customHeaders["X-Incremental"] = "$incremental"
		customHeaders["X-Uuid"] = "$uuid"

		pHeader := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		pHeader.ReverseProxyHandler(&ctx)

		_, err := uuid.Parse(string(ctx.Request.Header.Peek("X-Uuid")))
		assert.Nil(t, err)
		assert.Equal(t, ctx.RemoteIP().String(), string(ctx.Request.Header.Peek("X-Remote-Addr")))
		assert.Equal(t, "1", string(ctx.Request.Header.Peek("X-Incremental")))

		timeHeader := string(ctx.Request.Header.Peek("X-Time"))
		assert.NotEmpty(t, timeHeader, "X-Time header should be set")
		stamped, err := time.Parse(time.RFC3339Nano, timeHeader)
		assert.NoError(t, err, "X-Time header should be a valid RFC 3339 timestamp")
		assert.True(t, strings.HasSuffix(timeHeader, "Z"), "X-Time is stamped in UTC: %s", timeHeader)
		assert.WithinDuration(t, time.Now(), stamped, time.Minute, "a local-time value with a Z suffix is off by the zone offset")
	})

	t.Run("x-forwarded-for appends the peer to a client-sent chain", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.2")
		p.ReverseProxyHandler(&ctx)
		assert.Equal(t, "203.0.113.9, 198.51.100.2, 0.0.0.0", string(ctx.Request.Header.PeekBytes(XForwardedFor)))
	})

	t.Run("x-forwarded-for folds repeated header lines into one chain", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.Header.Add("X-Forwarded-For", "203.0.113.9")
		ctx.Request.Header.Add("X-Forwarded-For", "")
		ctx.Request.Header.Add("X-Forwarded-For", "198.51.100.2")
		p.ReverseProxyHandler(&ctx)
		assert.Equal(t, "203.0.113.9, 198.51.100.2, 0.0.0.0", string(ctx.Request.Header.PeekBytes(XForwardedFor)))
		assert.Len(t, ctx.Request.Header.PeekAll("X-Forwarded-For"), 1)
	})

	t.Run("default http", func(t *testing.T) {
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		p.ReverseProxyHandler(&ctx)
		assert.Equal(t, "http", string(ctx.Request.URI().Scheme()))
	})
}

func TestPendingRequests(t *testing.T) {

	customHeaders := make(map[string]string)
	handler := mockServer{done: make(chan struct{}), ready: make(chan struct{})}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)

	assert.Equal(t, 0, p.PendingRequests())
	concurrency := 10
	for range concurrency {
		go func() {
			ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
			ctx.Request.Header.Add("Pending", "true")
			p.ReverseProxyHandler(&ctx)
		}()
	}

	for range concurrency {
		<-handler.ready
	}

	close(handler.done)
	assert.Equal(t, concurrency, p.PendingRequests())
}

func TestClose(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)

	// Make a request to establish connection
	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	err := p.ReverseProxyHandler(&ctx)
	assert.NoError(t, err)

	// Verify connection is established
	stat := p.Stat()
	assert.Equal(t, 1, stat.ConnsCount)

	// Test Close method
	err = p.Close()
	assert.NoError(t, err, "Close() should not return an error")

	// Wait for connections to be closed with a timeout
	timeout := time.After(1 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var connectionsClosed bool
	for !connectionsClosed {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for connections to close")
		case <-ticker.C:
			statAfterClose := p.Stat()
			if statAfterClose.ConnsCount == 0 {
				connectionsClosed = true
			}
		}
	}

	// Verify that the proxy client still functions after Close()
	ctx2 := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	err = p.ReverseProxyHandler(&ctx2)
	assert.NoError(t, err, "Proxy should still work after Close()")

	// Test multiple Close calls (should be idempotent)
	err = p.Close()
	assert.NoError(t, err, "Multiple Close() calls should not return an error")
}

func TestMiddlewareOnRequestSuccess(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("should execute OnRequest and modify request headers", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Request.Header.Set("X-Test-Header", "test-value")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, "test-value", string(ctx.Request.Header.Peek("X-Test-Header")))
	})

	t.Run("should call OnRequest before proxying", func(t *testing.T) {
		var requestExecuted bool
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				requestExecuted = true
				// Set a marker to verify this runs before proxy
				ctx.Request.Header.Set("X-Before-Proxy", "true")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.True(t, requestExecuted)
		assert.Equal(t, 1, mw.getRequestCalls())
	})
}

func TestMiddlewareOnResponseSuccess(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("should execute OnResponse and modify response headers", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.Header.Set("X-Response-Header", "response-value")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, mw.getResponseCalls())
		assert.Equal(t, "response-value", string(ctx.Response.Header.Peek("X-Response-Header")))
	})

	t.Run("should call OnResponse after receiving backend response", func(t *testing.T) {
		var responseExecuted bool
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				responseExecuted = true
				// Verify we have a response (status code set)
				assert.Equal(t, 200, ctx.Response.StatusCode())
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.True(t, responseExecuted)
		assert.Equal(t, 1, mw.getResponseCalls())
	})

	t.Run("should call both OnRequest and OnResponse", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Request.Header.Set("X-Request-MW", "request")
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.Header.Set("X-Response-MW", "response")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, 1, mw.getResponseCalls())
		assert.Equal(t, "request", string(ctx.Request.Header.Peek("X-Request-MW")))
		assert.Equal(t, "response", string(ctx.Response.Header.Peek("X-Response-MW")))
	})
}

func TestMiddlewareOnRequestError(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("should return error from OnRequest", func(t *testing.T) {
		expectedErr := errors.New("middleware error")
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return expectedErr
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Equal(t, 1, mw.getRequestCalls())
	})

	t.Run("should not call OnResponse when OnRequest fails", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return errors.New("request failed")
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				t.Error("OnResponse should not be called when OnRequest fails")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, 0, mw.getResponseCalls())
	})

	t.Run("should not proxy to backend when OnRequest fails", func(t *testing.T) {
		// Use invalid backend to ensure we're not reaching it
		invalidBackend := config.Backend{Url: "invalid:99999"}
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return errors.New("blocked by middleware")
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, "blocked by middleware", err.Error())
		// Verify we got middleware error, not connection error
		assert.NotContains(t, err.Error(), "connection")
	})

	t.Run("should stop at first middleware error", func(t *testing.T) {
		mw1 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return errors.New("first middleware error")
			},
		}
		mw2 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				t.Error("Second middleware should not be called")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw1, mw2)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, 1, mw1.getRequestCalls())
		assert.Equal(t, 0, mw2.getRequestCalls())
	})
}

func TestMiddlewareExecutionOrder(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("should execute multiple middlewares in order (OnRequest)", func(t *testing.T) {
		var executionOrder []string
		var mu sync.Mutex

		mw1 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw1-request")
				mu.Unlock()
				return nil
			},
		}
		mw2 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw2-request")
				mu.Unlock()
				return nil
			},
		}
		mw3 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw3-request")
				mu.Unlock()
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw1, mw2, mw3)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"mw1-request", "mw2-request", "mw3-request"}, executionOrder)
	})

	t.Run("should execute multiple middlewares in order (OnResponse)", func(t *testing.T) {
		var executionOrder []string
		var mu sync.Mutex

		mw1 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw1-response")
				mu.Unlock()
				return nil
			},
		}
		mw2 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw2-response")
				mu.Unlock()
				return nil
			},
		}
		mw3 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw3-response")
				mu.Unlock()
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw1, mw2, mw3)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"mw1-response", "mw2-response", "mw3-response"}, executionOrder)
	})

	t.Run("should execute in correct flow order: OnRequest -> Backend -> OnResponse", func(t *testing.T) {
		var executionOrder []string
		var mu sync.Mutex

		mw1 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw1-request")
				mu.Unlock()
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw1-response")
				mu.Unlock()
				return nil
			},
		}
		mw2 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw2-request")
				mu.Unlock()
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				mu.Lock()
				executionOrder = append(executionOrder, "mw2-response")
				mu.Unlock()
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw1, mw2)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		// Verify the order: all OnRequest calls happen first, then backend (implicit), then all OnResponse calls
		assert.Equal(t, []string{"mw1-request", "mw2-request", "mw1-response", "mw2-response"}, executionOrder)
	})

	t.Run("should allow middlewares to modify data for subsequent middlewares", func(t *testing.T) {
		mw1 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Request.Header.Set("X-Chain", "mw1")
				return nil
			},
		}
		mw2 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				existing := string(ctx.Request.Header.Peek("X-Chain"))
				ctx.Request.Header.Set("X-Chain", existing+"-mw2")
				return nil
			},
		}
		mw3 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				existing := string(ctx.Request.Header.Peek("X-Chain"))
				ctx.Request.Header.Set("X-Chain", existing+"-mw3")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw1, mw2, mw3)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, "mw1-mw2-mw3", string(ctx.Request.Header.Peek("X-Chain")))
	})
}

func TestMiddlewareEdgeCases(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("should work normally with nil middlewareExecutor", func(t *testing.T) {
		p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	})

	t.Run("should not panic with nil middlewareExecutor on concurrent requests", func(t *testing.T) {
		p := NewProxyClient(&backend, customHeaders, nil).(*ProxyClient)
		var wg sync.WaitGroup
		concurrency := 10

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
				err := p.ReverseProxyHandler(&ctx)
				assert.NoError(t, err)
			}()
		}

		wg.Wait()
	})

	t.Run("should handle concurrent requests with middleware", func(t *testing.T) {
		var counter int64
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				atomic.AddInt64(&counter, 1)
				ctx.Request.Header.Set("X-Counter", strconv.FormatInt(atomic.LoadInt64(&counter), 10))
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.Header.Set("X-Processed", "true")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		var wg sync.WaitGroup
		concurrency := 20
		successCount := int64(0)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
				err := p.ReverseProxyHandler(&ctx)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
					assert.Equal(t, "true", string(ctx.Response.Header.Peek("X-Processed")))
				}
			}()
		}

		wg.Wait()
		assert.Equal(t, int64(concurrency), successCount)
		assert.Equal(t, int64(concurrency), atomic.LoadInt64(&counter))
	})

	t.Run("should provide isolated context for each request", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				// Each request should get its own context
				ctx.Request.Header.Set("X-Request-ID", uuid.New().String())
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)

		var mu sync.Mutex
		requestIDs := make(map[string]bool)
		var wg sync.WaitGroup
		concurrency := 10

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
				err := p.ReverseProxyHandler(&ctx)
				assert.NoError(t, err)

				requestID := string(ctx.Request.Header.Peek("X-Request-ID"))
				mu.Lock()
				assert.False(t, requestIDs[requestID], "Request ID should be unique")
				requestIDs[requestID] = true
				mu.Unlock()
			}()
		}

		wg.Wait()
		assert.Equal(t, concurrency, len(requestIDs))
	})

	t.Run("should handle middleware that does nothing", func(t *testing.T) {
		mw := &mockMiddleware{
			// No functions set - should just track calls
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, 1, mw.getResponseCalls())
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	})

	t.Run("should call OnResponse with error when backend proxy fails", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				assert.Error(t, err)
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, 1, mw.getResponseCalls()) // OnResponse should be called with the error
	})
}

func newTestRequestCtx() *fasthttp.RequestCtx {
	return &fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
}

func TestMiddlewareShortCircuit(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	unreachableBackend := config.Backend{Url: "invalid-host:99999"}

	t.Run("OnResponse short-circuit replaces the default 502 with the middleware's response", func(t *testing.T) {
		var receivedError error
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				receivedError = err
				ctx.Response.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.Response.Header.Set("Content-Type", "application/json")
				ctx.Response.Header.Set("Retry-After", "60")
				ctx.Response.SetBodyString(`{"error":"backend unavailable"}`)
				return middleware.ErrShortCircuit
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		err := p.ReverseProxyHandler(ctx)
		assert.Error(t, err, "the handler still reports the Backend failure")
		assert.NotErrorIs(t, err, middleware.ErrShortCircuit)
		assert.Error(t, receivedError)
		assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
		assert.Equal(t, "60", string(ctx.Response.Header.Peek("Retry-After")))
		assert.Equal(t, `{"error":"backend unavailable"}`, string(ctx.Response.Body()))
		assert.False(t, ctx.Response.ConnectionClose(), "Connection: close belongs to divisor's own 502, not a short-circuit")
	})

	t.Run("a 200 fallback on a Backend failure goes out as written", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					ctx.Response.SetBodyString("cached page")
					return middleware.ErrShortCircuit
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		p.ReverseProxyHandler(ctx) //nolint:errcheck
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
		assert.Equal(t, "cached page", string(ctx.Response.Body()))
	})

	t.Run("a wrapped ErrShortCircuit counts", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.Response.SetBodyString("fallback")
				return fmt.Errorf("cache hit: %w", middleware.ErrShortCircuit)
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		p.ReverseProxyHandler(ctx) //nolint:errcheck
		assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
		assert.Equal(t, "fallback", string(ctx.Response.Body()))
	})

	t.Run("the first short-circuit stops later OnResponse middlewares", func(t *testing.T) {
		mw1 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.SetStatusCode(fasthttp.StatusBadGateway)
				ctx.Response.SetBodyString("handled by mw1")
				return middleware.ErrShortCircuit
			},
		}
		mw2 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				t.Error("second middleware OnResponse must not run after a short-circuit")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw1, mw2)
		ctx := newTestRequestCtx()

		p.ReverseProxyHandler(ctx) //nolint:errcheck
		assert.Equal(t, 1, mw1.getResponseCalls())
		assert.Equal(t, 0, mw2.getResponseCalls())
		assert.Equal(t, "handled by mw1", string(ctx.Response.Body()))
	})

	t.Run("OnRequest short-circuit sends the middleware's response and asks no Backend", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Response.SetStatusCode(fasthttp.StatusForbidden)
				ctx.Response.SetBodyString("blocked")
				return middleware.ErrShortCircuit
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				t.Error("OnResponse must not run after an OnRequest short-circuit")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		err := p.ReverseProxyHandler(ctx)
		assert.NoError(t, err, "no Backend was asked, nothing failed")
		assert.Equal(t, fasthttp.StatusForbidden, ctx.Response.StatusCode())
		assert.Equal(t, "blocked", string(ctx.Response.Body()))
		assert.Equal(t, 0, mw.getResponseCalls())
	})

	t.Run("OnRequest short-circuit may answer 200", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Response.SetBodyString("served from cache")
				return middleware.ErrShortCircuit
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		assert.NoError(t, p.ReverseProxyHandler(ctx))
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
		assert.Equal(t, "served from cache", string(ctx.Response.Body()))
	})

	t.Run("nil after a Backend failure lets the default 502 run", func(t *testing.T) {
		var observed error
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				observed = err
				ctx.Response.Header.Set("X-Request-Id", "abc")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		err := p.ReverseProxyHandler(ctx)
		assert.Error(t, err)
		assert.Error(t, observed)
		assert.Equal(t, fasthttp.StatusBadGateway, ctx.Response.StatusCode())
		assert.JSONEq(t, `{"message":"bad gateway"}`, string(ctx.Response.Body()))
		assert.Equal(t, "abc", string(ctx.Response.Header.Peek("X-Request-Id")), "headers a middleware adds on the error path survive the default 502")
	})

	t.Run("every middleware observes a Backend failure when none short-circuits", func(t *testing.T) {
		var saw [3]bool
		var mws []middleware.Middleware
		for i := range saw {
			mws = append(mws, &mockMiddleware{
				onResponseFunc: func(ctx *middleware.Context, err error) error {
					saw[i] = err != nil
					return nil
				},
			})
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mws...)
		ctx := newTestRequestCtx()

		p.ReverseProxyHandler(ctx) //nolint:errcheck
		assert.Equal(t, [3]bool{true, true, true}, saw)
		assert.Equal(t, fasthttp.StatusBadGateway, ctx.Response.StatusCode())
	})

	t.Run("OnResponse receives nil and may mutate a successful response", func(t *testing.T) {
		var received error = errors.New("unset")
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				received = err
				ctx.Response.Header.Set("X-Success-Handler", "true")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := newTestRequestCtx()

		assert.NoError(t, p.ReverseProxyHandler(ctx))
		assert.NoError(t, received)
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
		assert.Equal(t, "true", string(ctx.Response.Header.Peek("X-Success-Handler")))
	})

	t.Run("a short-circuited Backend success is still scored", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				ctx.Response.SetBodyString("rewritten")
				return middleware.ErrShortCircuit
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := newTestRequestCtx()

		assert.NoError(t, p.ReverseProxyHandler(ctx))
		assert.Equal(t, "rewritten", string(ctx.Response.Body()))
		assert.Equal(t, uint64(1), atomic.LoadUint64(p.measuredRequestCount))
		assert.Greater(t, p.RecentResponseTime(), 0.0)
	})
}

func TestMiddlewareErrorReachesClient(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")
	unreachableBackend := config.Backend{Url: "invalid-host:99999"}

	t.Run("OnRequest error answers 500", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return errors.New("unauthorized")
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		err := p.ReverseProxyHandler(ctx)
		assert.Error(t, err)
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.Equal(t, "application/json", string(ctx.Response.Header.Peek("Content-Type")))
		assert.JSONEq(t, `{"message":"unauthorized"}`, string(ctx.Response.Body()))
	})

	t.Run("OnRequest error discards a response the middleware wrote itself", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				ctx.Response.SetStatusCode(fasthttp.StatusUnauthorized)
				ctx.Response.Header.Set("X-Crafted", "1")
				ctx.Response.SetBodyString("no api key")
				return errors.New("unauthorized")
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		assert.Error(t, p.ReverseProxyHandler(ctx))
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.Empty(t, ctx.Response.Header.Peek("X-Crafted"))
		assert.JSONEq(t, `{"message":"unauthorized"}`, string(ctx.Response.Body()))
	})

	t.Run("OnResponse error after a Backend failure answers 500, not 502", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				return errors.New("rejected by middleware")
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		err := p.ReverseProxyHandler(ctx)
		assert.Error(t, err)
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.JSONEq(t, `{"message":"rejected by middleware"}`, string(ctx.Response.Body()))
	})

	t.Run("OnResponse error after a Backend success discards the Backend response", func(t *testing.T) {
		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				return errors.New("response failed validation")
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := newTestRequestCtx()
		ctx.Request.Header.Set("Stamp", "1")

		err := p.ReverseProxyHandler(ctx)
		assert.Error(t, err)
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.Empty(t, ctx.Response.Header.Peek("X-Backend-Stamp"), "Backend headers must not leak past a failed middleware")
		assert.JSONEq(t, `{"message":"response failed validation"}`, string(ctx.Response.Body()))
	})

	t.Run("error message with quotes stays valid JSON", func(t *testing.T) {
		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return errors.New(`token "abc" is \invalid`)
			},
		}

		p := createTestProxyWithMiddlewares(unreachableBackend, customHeaders, mw)
		ctx := newTestRequestCtx()

		assert.Error(t, p.ReverseProxyHandler(ctx))

		var body struct {
			Message string `json:"message"`
		}
		assert.NoError(t, json.Unmarshal(ctx.Response.Body(), &body))
		assert.Equal(t, `token "abc" is \invalid`, body.Message)
	})
}

func TestResponseTimeSubMillisecond(t *testing.T) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()

	b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	assert.NoError(t, p.ReverseProxyHandler(&ctx))

	// A localhost backend answers in well under a millisecond; whole-millisecond
	// accounting rounded that to zero and made every backend look equally fast.
	assert.Greater(t, p.AvgResponseTime(), float64(0))
	assert.Greater(t, p.RecentResponseTime(), float64(0))
}

func TestRecentResponseTimeDecays(t *testing.T) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)

	assert.Equal(t, float64(0), p.RecentResponseTime())

	p.recordResponseTime(100 * time.Millisecond)
	assert.InDelta(t, 100, p.RecentResponseTime(), 0.001)

	for i := 0; i < 20; i++ {
		p.recordResponseTime(time.Millisecond)
	}

	// One slow response must not deprioritize the backend forever.
	assert.Less(t, p.RecentResponseTime(), float64(3))
	assert.Greater(t, p.RecentResponseTime(), float64(1))
}

func TestFailedRequestPenalizesRecentResponseTime(t *testing.T) {
	p := NewProxyClient(&config.Backend{Url: "invalid-host:99999"}, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	assert.Error(t, p.ReverseProxyHandler(&ctx))

	// A refused connection fails in microseconds; scored by elapsed time the
	// failing backend would rank as the fastest in the pool.
	penaltyMs := failureResponseTimePenalty.Seconds() * 1000
	assert.GreaterOrEqual(t, p.RecentResponseTime(), penaltyMs)
	assert.Equal(t, float64(0), p.AvgResponseTime())
}

func TestFailurePenaltyOutweighsHealthyHistory(t *testing.T) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)

	p.recordResponseTime(time.Millisecond)
	p.recordFailure(500 * time.Microsecond)

	penaltyMs := failureResponseTimePenalty.Seconds() * 1000
	expected := responseTimeSmoothing*penaltyMs + (1-responseTimeSmoothing)*1
	assert.InDelta(t, expected, p.RecentResponseTime(), 1)
}

func TestResetRecentResponseTime(t *testing.T) {
	p := NewProxyClient(&config.Backend{Url: "localhost:8080"}, nil, nil).(*ProxyClient)

	p.recordFailure(time.Millisecond)
	assert.Greater(t, p.RecentResponseTime(), float64(0))

	p.ResetRecentResponseTime()
	assert.Equal(t, float64(0), p.RecentResponseTime())

	// The first sample after a reset stands alone instead of smoothing
	// against the discarded score.
	p.recordResponseTime(2 * time.Millisecond)
	assert.InDelta(t, 2, p.RecentResponseTime(), 0.001)
}

func TestAvgResponseTimeExcludesFailures(t *testing.T) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	assert.NoError(t, p.ReverseProxyHandler(&ctx))
	avg := p.AvgResponseTime()
	assert.Greater(t, avg, float64(0))

	bServer.Close()
	ctx = fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	assert.Error(t, p.ReverseProxyHandler(&ctx))

	// A failure adds to neither numerator nor denominator: the lifetime
	// average covers successful requests only.
	assert.Equal(t, avg, p.AvgResponseTime())
	assert.Equal(t, uint64(2), p.Stat().TotalReqCount)
}

func TestConnectionNominatedHeadersStripped(t *testing.T) {
	t.Run("request: headers the client nominates do not reach the backend", func(t *testing.T) {
		var seen http.Header
		bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Clone()
		}))
		defer bServer.Close()

		b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
		p := NewProxyClient(&b, nil, nil).(*ProxyClient)

		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.Header.Set("Connection", "X-Secret, x-also-secret ,close")
		ctx.Request.Header.Set("X-Secret", "s3cr3t")
		ctx.Request.Header.Set("X-Also-Secret", "too")
		ctx.Request.Header.Set("X-Stays", "yes")

		assert.NoError(t, p.ReverseProxyHandler(&ctx))
		assert.Empty(t, seen.Get("X-Secret"))
		assert.Empty(t, seen.Get("X-Also-Secret"))
		assert.Empty(t, seen.Get("Connection"))
		assert.Equal(t, "yes", seen.Get("X-Stays"))
	})

	t.Run("request: nominating Host or X-Forwarded-For cannot displace divisor's values", func(t *testing.T) {
		var seen http.Header
		var seenHost string
		bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Clone()
			seenHost = r.Host
		}))
		defer bServer.Close()

		b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
		p := NewProxyClient(&b, nil, nil).(*ProxyClient)

		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.SetHost("spoofed.example")
		ctx.Request.Header.Set("Connection", "Host, X-Forwarded-For")
		ctx.Request.Header.Set("X-Forwarded-For", "1.2.3.4")

		assert.NoError(t, p.ReverseProxyHandler(&ctx))
		assert.Equal(t, b.Url, seenHost)
		assert.Equal(t, "0.0.0.0", seen.Get("X-Forwarded-For"))
	})

	t.Run("request: nominating Content-Length keeps the body framed", func(t *testing.T) {
		var seenBody string
		bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			seenBody = string(body)
		}))
		defer bServer.Close()

		b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
		p := NewProxyClient(&b, nil, nil).(*ProxyClient)

		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		ctx.Request.Header.SetMethod(fasthttp.MethodPost)
		ctx.Request.Header.Set("Connection", "Content-Length")
		ctx.Request.SetBodyStream(strings.NewReader("payload"), len("payload"))

		assert.NoError(t, p.ReverseProxyHandler(&ctx))
		assert.Equal(t, "payload", seenBody)
	})

	t.Run("response: headers the backend nominates do not reach the client", func(t *testing.T) {
		bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Connection", "X-Internal-Debug,Keep-Alive")
			w.Header().Set("X-Internal-Debug", "pool=3 shard=7")
			w.Header().Set("X-Resp-Stays", "1")
		}))
		defer bServer.Close()

		b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
		p := NewProxyClient(&b, nil, nil).(*ProxyClient)

		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
		assert.NoError(t, p.ReverseProxyHandler(&ctx))
		assert.Empty(t, ctx.Response.Header.Peek("X-Internal-Debug"))
		assert.Empty(t, ctx.Response.Header.Peek("Connection"))
		assert.Equal(t, "1", string(ctx.Response.Header.Peek("X-Resp-Stays")))
	})

	t.Run("several Connection header lines are all honoured", func(t *testing.T) {
		// Programmatic Add overwrites Connection in fasthttp; only a parsed
		// response carries several lines, which is how a backend sends them.
		raw := "HTTP/1.1 200 OK\r\nConnection: X-One\r\nConnection: X-Two\r\nX-One: 1\r\nX-Two: 2\r\nContent-Length: 0\r\n\r\n"
		res := fasthttp.AcquireResponse()
		assert.NoError(t, res.Read(bufio.NewReader(strings.NewReader(raw))))
		assert.Len(t, res.Header.PeekAll("Connection"), 2)

		delConnectionNominated(&res.Header)
		assert.Empty(t, res.Header.Peek("X-One"))
		assert.Empty(t, res.Header.Peek("X-Two"))
	})
}

// The sequence number is the return value of the counter increment, not a
// later re-read: under concurrency a re-read hands the same value to several
// requests and skips others.
func TestIncrementalHeaderUniquePerRequest(t *testing.T) {
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()

	b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&b, map[string]string{"X-Incremental": "$incremental"}, nil).(*ProxyClient)

	const requestCount = 1000
	sequenceNumbers := make([]uint64, requestCount)
	var wg sync.WaitGroup
	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
			_ = p.ReverseProxyHandler(&ctx)
			sequenceNumber, err := strconv.ParseUint(string(ctx.Request.Header.Peek("X-Incremental")), 10, 64)
			assert.NoError(t, err)
			sequenceNumbers[i] = sequenceNumber
		}(i)
	}
	wg.Wait()

	seen := make(map[uint64]struct{}, requestCount)
	for _, sequenceNumber := range sequenceNumbers {
		assert.GreaterOrEqual(t, sequenceNumber, uint64(1))
		assert.LessOrEqual(t, sequenceNumber, uint64(requestCount))
		_, duplicate := seen[sequenceNumber]
		assert.False(t, duplicate, "sequence number %d handed to two requests", sequenceNumber)
		seen[sequenceNumber] = struct{}{}
	}
	assert.Len(t, seen, requestCount)
	assert.Equal(t, uint64(requestCount), p.Stat().TotalReqCount)
}
