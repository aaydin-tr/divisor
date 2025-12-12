package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
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
		assert.Contains(t, string(ctx.Response.Body()), err.Error())
		assert.Equal(t, ctx.Response.StatusCode(), fasthttp.StatusInternalServerError)
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
		_, err = time.Parse("2006-01-02T15:04:05.000Z", timeHeader)
		assert.NoError(t, err, "X-Time header should be a valid timestamp")
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

func TestMiddlewareErrorHandling(t *testing.T) {
	customHeaders := make(map[string]string)
	handler := mockServer{}
	bServer := httptest.NewServer(&handler)
	defer bServer.Close()
	backend.Url = protocolRegex.ReplaceAllString(bServer.URL, "")

	t.Run("middleware handles error and skips default error handler", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}
		var receivedError error

		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				receivedError = err
				// Middleware handles the error by setting custom response
				ctx.Response.SetStatusCode(fasthttp.StatusServiceUnavailable)
				ctx.Response.Header.Set("Content-Type", "application/json")
				ctx.Response.SetBodyString(`{"error":"custom error handling","message":"backend unavailable"}`)
				// Return non-nil to signal error was handled
				return err
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.NotNil(t, receivedError)
		assert.Equal(t, 1, mw.getRequestCalls())
		assert.Equal(t, 1, mw.getResponseCalls())

		// Verify custom response (not default 500)
		assert.Equal(t, fasthttp.StatusServiceUnavailable, ctx.Response.StatusCode())
		assert.Contains(t, string(ctx.Response.Body()), "custom error handling")
		assert.Contains(t, string(ctx.Response.Body()), "backend unavailable")
		// Verify it's our custom message, not the default error handler format
		assert.Equal(t, `{"error":"custom error handling","message":"backend unavailable"}`, string(ctx.Response.Body()))
	})

	t.Run("middleware does not handle error and default error handler runs", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}
		var receivedError error
		var middlewareExecuted bool

		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				receivedError = err
				middlewareExecuted = true
				// Middleware observes but doesn't handle - returns nil
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.NotNil(t, receivedError)
		assert.True(t, middlewareExecuted)
		assert.Equal(t, 1, mw.getResponseCalls())

		// Verify default error handler ran (500 status code)
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		// Default handler sets JSON with "message" field
		assert.Contains(t, string(ctx.Response.Body()), `"message":"`)
		assert.Equal(t, "application/json", string(ctx.Response.Header.Peek("Content-Type")))
	})

	t.Run("multiple middlewares - first handles error stops subsequent OnResponse", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}

		mw1 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					// First middleware handles the error
					ctx.Response.SetStatusCode(fasthttp.StatusBadGateway)
					ctx.Response.SetBodyString("handled by mw1")
					return err // Signal error was handled
				}
				return nil
			},
		}

		mw2 := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				// This should NOT be called because mw1 handled the error
				t.Error("Second middleware OnResponse should not be called when first handles error")
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw1, mw2)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.Equal(t, 1, mw1.getResponseCalls())
		assert.Equal(t, 0, mw2.getResponseCalls()) // Should not be called

		// Verify first middleware's response
		assert.Equal(t, fasthttp.StatusBadGateway, ctx.Response.StatusCode())
		assert.Equal(t, "handled by mw1", string(ctx.Response.Body()))
	})

	t.Run("middleware sets custom error response with specific status code", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}

		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					// Custom error handling with specific status
					ctx.Response.SetStatusCode(503)
					ctx.Response.Header.Set("Content-Type", "text/plain")
					ctx.Response.Header.Set("X-Error-Handler", "custom-middleware")
					ctx.Response.SetBodyString("Service temporarily unavailable. Please try again later.")
					return err
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)

		// Verify custom response
		assert.Equal(t, 503, ctx.Response.StatusCode())
		assert.Equal(t, "text/plain", string(ctx.Response.Header.Peek("Content-Type")))
		assert.Equal(t, "custom-middleware", string(ctx.Response.Header.Peek("X-Error-Handler")))
		assert.Equal(t, "Service temporarily unavailable. Please try again later.", string(ctx.Response.Body()))
	})

	t.Run("middleware receives nil error on successful request", func(t *testing.T) {
		var receivedError error
		var wasSuccess bool

		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				receivedError = err
				if err == nil {
					wasSuccess = true
					ctx.Response.Header.Set("X-Success-Handler", "true")
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(backend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.NoError(t, err)
		assert.Nil(t, receivedError) // Error should be nil on success
		assert.True(t, wasSuccess)
		assert.Equal(t, 1, mw.getResponseCalls())

		// Verify successful response
		assert.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
		assert.Equal(t, "true", string(ctx.Response.Header.Peek("X-Success-Handler")))
	})

	t.Run("middleware observes and logs error but lets default handler run", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}
		var errorLogged bool
		var loggedErrorMessage string

		mw := &mockMiddleware{
			onRequestFunc: func(ctx *middleware.Context) error {
				return nil
			},
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					// Middleware logs/observes the error
					errorLogged = true
					loggedErrorMessage = err.Error()
					// But doesn't handle it - returns nil
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)
		assert.True(t, errorLogged)
		assert.NotEmpty(t, loggedErrorMessage)
		assert.Equal(t, 1, mw.getResponseCalls())

		// Verify default error handler ran
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
		assert.Contains(t, string(ctx.Response.Body()), `"message":"`)
	})

	t.Run("multiple middlewares all observe error before default handler", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}
		var mw1Saw, mw2Saw, mw3Saw bool

		mw1 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					mw1Saw = true
					ctx.Response.Header.Add("X-Middleware-Order", "mw1")
				}
				return nil
			},
		}

		mw2 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					mw2Saw = true
					ctx.Response.Header.Add("X-Middleware-Order", "mw2")
				}
				return nil
			},
		}

		mw3 := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					mw3Saw = true
					ctx.Response.Header.Add("X-Middleware-Order", "mw3")
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw1, mw2, mw3)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)

		// All middlewares should have observed the error
		assert.True(t, mw1Saw)
		assert.True(t, mw2Saw)
		assert.True(t, mw3Saw)
		assert.Equal(t, 1, mw1.getResponseCalls())
		assert.Equal(t, 1, mw2.getResponseCalls())
		assert.Equal(t, 1, mw3.getResponseCalls())

		// Default handler should have run
		assert.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
	})

	t.Run("middleware can modify response even when handling error", func(t *testing.T) {
		invalidBackend := config.Backend{Url: "invalid-host:99999"}

		mw := &mockMiddleware{
			onResponseFunc: func(ctx *middleware.Context, err error) error {
				if err != nil {
					// Set multiple headers
					ctx.Response.Header.Set("X-Error-Handled", "true")
					ctx.Response.Header.Set("X-Error-Time", "2024-01-01")
					ctx.Response.Header.Set("Retry-After", "60")

					// Set custom body
					ctx.Response.SetStatusCode(429)
					ctx.Response.SetBodyString(`{"error":"rate_limit","retry_after":60}`)

					return err // Handle the error
				}
				return nil
			},
		}

		p := createTestProxyWithMiddlewares(invalidBackend, customHeaders, mw)
		ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}

		err := p.ReverseProxyHandler(&ctx)
		assert.Error(t, err)

		// Verify all custom headers and body
		assert.Equal(t, 429, ctx.Response.StatusCode())
		assert.Equal(t, "true", string(ctx.Response.Header.Peek("X-Error-Handled")))
		assert.Equal(t, "2024-01-01", string(ctx.Response.Header.Peek("X-Error-Time")))
		assert.Equal(t, "60", string(ctx.Response.Header.Peek("Retry-After")))
		assert.Contains(t, string(ctx.Response.Body()), "rate_limit")
		assert.Contains(t, string(ctx.Response.Body()), "retry_after")
	})
}
