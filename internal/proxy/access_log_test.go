package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// observeAccessLog swaps the Access log for an observer core (the agreed test
// seam, .scratch/logging/spec.md) and restores it when the test ends.
func observeAccessLog(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, observed := observer.New(zapcore.InfoLevel)
	t.Cleanup(logger.ReplaceAccessLogger(zap.New(core)))
	return observed
}

func accessLogRequestCtx(method, uri string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	return ctx
}

func singleAccessLogEntry(t *testing.T, observed *observer.ObservedLogs) map[string]any {
	t.Helper()
	require.Equal(t, 1, observed.Len(), "expected exactly one Access log line")
	return observed.All()[0].ContextMap()
}

func TestAccessLogOnSuccessfulProxy(t *testing.T) {
	handler := mockServer{}
	backendServer := httptest.NewServer(&handler)
	defer backendServer.Close()
	b := config.Backend{Url: protocolRegex.ReplaceAllString(backendServer.URL, "")}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	observed := observeAccessLog(t)
	ctx := accessLogRequestCtx("GET", "/api/test?x=1")
	require.NoError(t, p.ReverseProxyHandler(ctx))

	fields := singleAccessLogEntry(t, observed)
	assert.Equal(t, ctx.RemoteIP().String(), fields["client_ip"])
	assert.Equal(t, "GET", fields["method"])
	assert.Equal(t, "/api/test", fields["path"], "path carries no query string")
	assert.Equal(t, int64(200), fields["status"])
	assert.Equal(t, b.Url, fields["backend"])
	assert.Equal(t, uint64(1), fields["request_seq"])
	assert.GreaterOrEqual(t, fields["duration_ms"], float64(0))
	assert.Equal(t, int64(len(ctx.Response.Body())), fields["bytes_out"])
	assert.NotContains(t, fields, "short_circuit")
}

func TestAccessLogOnFailedProxyAttempt(t *testing.T) {
	b := config.Backend{Url: "localhost:1"}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	observed := observeAccessLog(t)
	ctx := accessLogRequestCtx("POST", "/orders")
	assert.Error(t, p.ReverseProxyHandler(ctx))

	fields := singleAccessLogEntry(t, observed)
	assert.Equal(t, int64(fasthttp.StatusBadGateway), fields["status"])
	assert.Equal(t, "localhost:1", fields["backend"])
	assert.Equal(t, uint64(1), fields["request_seq"])
	assert.Equal(t, int64(len(ctx.Response.Body())), fields["bytes_out"])
	assert.NotContains(t, fields, "short_circuit")
}

func TestAccessLogOnMiddlewareShortCircuit(t *testing.T) {
	t.Run("OnRequest answers the client", func(t *testing.T) {
		shortCircuiting := &mockMiddleware{
			onRequestFunc: func(mwCtx *middleware.Context) error {
				mwCtx.Response.SetStatusCode(fasthttp.StatusForbidden)
				mwCtx.Response.SetBodyString(`{"message":"blocked"}`)
				return middleware.ErrShortCircuit
			},
		}
		p := createTestProxyWithMiddlewares(config.Backend{Url: "localhost:1"}, nil, shortCircuiting)

		observed := observeAccessLog(t)
		ctx := accessLogRequestCtx("GET", "/blocked")
		require.NoError(t, p.ReverseProxyHandler(ctx))

		fields := singleAccessLogEntry(t, observed)
		assert.Equal(t, true, fields["short_circuit"])
		assert.Equal(t, int64(fasthttp.StatusForbidden), fields["status"])
		assert.Equal(t, "localhost:1", fields["backend"])
		assert.Equal(t, uint64(1), fields["request_seq"])
	})

	t.Run("OnResponse replaces a failed attempt", func(t *testing.T) {
		shortCircuiting := &mockMiddleware{
			onResponseFunc: func(mwCtx *middleware.Context, err error) error {
				mwCtx.Response.SetStatusCode(fasthttp.StatusOK)
				mwCtx.Response.SetBodyString("fallback")
				return middleware.ErrShortCircuit
			},
		}
		p := createTestProxyWithMiddlewares(config.Backend{Url: "localhost:1"}, nil, shortCircuiting)

		observed := observeAccessLog(t)
		ctx := accessLogRequestCtx("GET", "/fallback")
		assert.Error(t, p.ReverseProxyHandler(ctx), "handler reports the proxy outcome")

		fields := singleAccessLogEntry(t, observed)
		assert.Equal(t, true, fields["short_circuit"])
		assert.Equal(t, int64(fasthttp.StatusOK), fields["status"])
	})
}

func TestAccessLogRecordsMethodAndPathAsClientSentThem(t *testing.T) {
	rewriting := &mockMiddleware{
		onRequestFunc: func(mwCtx *middleware.Context) error {
			mwCtx.Request.Header.SetMethod("POST")
			mwCtx.Request.URI().SetPath("/rewritten")
			mwCtx.Response.SetStatusCode(fasthttp.StatusOK)
			return middleware.ErrShortCircuit
		},
	}
	p := createTestProxyWithMiddlewares(config.Backend{Url: "localhost:1"}, nil, rewriting)

	observed := observeAccessLog(t)
	ctx := accessLogRequestCtx("GET", "/as-sent")
	require.NoError(t, p.ReverseProxyHandler(ctx))

	fields := singleAccessLogEntry(t, observed)
	assert.Equal(t, "GET", fields["method"], "method is the one the client sent, not the Middleware rewrite")
	assert.Equal(t, "/as-sent", fields["path"], "path is the one the client sent, not the Middleware rewrite")
}

func TestAccessLogDoesNotBufferAStreamedBody(t *testing.T) {
	streaming := &mockMiddleware{
		onRequestFunc: func(mwCtx *middleware.Context) error {
			mwCtx.Response.SetStatusCode(fasthttp.StatusOK)
			mwCtx.Response.SetBodyStream(strings.NewReader("streamed"), -1)
			return middleware.ErrShortCircuit
		},
	}
	p := createTestProxyWithMiddlewares(config.Backend{Url: "localhost:1"}, nil, streaming)

	observed := observeAccessLog(t)
	ctx := accessLogRequestCtx("GET", "/stream")
	require.NoError(t, p.ReverseProxyHandler(ctx))

	fields := singleAccessLogEntry(t, observed)
	assert.Equal(t, int64(0), fields["bytes_out"], "a streamed body's size is unknown at emission")
	assert.True(t, ctx.Response.IsBodyStream(), "emitting the Access log must not consume the body stream")
}

func TestAccessLogOnZeroAliveBackends(t *testing.T) {
	observed := observeAccessLog(t)
	ctx := accessLogRequestCtx("GET", "/during-outage")
	NoAliveBackends(ctx)

	fields := singleAccessLogEntry(t, observed)
	assert.Equal(t, int64(fasthttp.StatusServiceUnavailable), fields["status"])
	assert.NotContains(t, fields, "backend")
	assert.NotContains(t, fields, "request_seq")
	assert.NotContains(t, fields, "short_circuit")
	assert.Equal(t, ctx.RemoteIP().String(), fields["client_ip"])
	assert.Equal(t, int64(len(ctx.Response.Body())), fields["bytes_out"])
}
