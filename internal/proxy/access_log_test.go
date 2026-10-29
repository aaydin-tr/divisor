package proxy

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// accessLogLinesFor keeps a test's lines apart from those of streams other
// tests left ending in the background: every test's Backend has its own address.
func accessLogLinesFor(observed *observer.ObservedLogs, backendAddr string) []observer.LoggedEntry {
	return observed.FilterField(zap.String("backend", backendAddr)).All()
}

func requireAccessLoggedOnce(t *testing.T, observed *observer.ObservedLogs, backendAddr string) map[string]any {
	t.Helper()
	require.Eventually(t, func() bool { return len(accessLogLinesFor(observed, backendAddr)) > 0 }, waitLimit, 10*time.Millisecond, "no Access log line")
	lines := accessLogLinesFor(observed, backendAddr)
	require.Len(t, lines, 1, "expected exactly one Access log line")
	return lines[0].ContextMap()
}

// A Streamed response is answered only when its stream ends, so that is when
// its line is written, covering the whole stream.
func TestStreamedResponseIsAccessLoggedAtStreamEnd(t *testing.T) {
	const openFor = 200 * time.Millisecond
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		observed := observeAccessLog(t)
		releaseLastFrame := make(chan struct{})
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeFrame(w, "data: 0\n")
			if waitFor(r, releaseLastFrame) {
				writeFrame(w, "data: 1\n")
			}
		})
		f := startFrontend(t, newStreamingProxyClient(backendAddr))
		f.url += "/events?topic=a"

		body := bufio.NewReader(getWithin(t, f).Body)
		assert.Equal(t, "data: 0\n", readFrameWithin(t, body))
		time.Sleep(openFor)
		assert.Empty(t, accessLogLinesFor(observed, backendAddr), "an open stream has no line yet")

		close(releaseLastFrame)
		_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(body) })
		require.NoError(t, err)

		fields := requireAccessLoggedOnce(t, observed, backendAddr)
		assert.Equal(t, "127.0.0.1", fields["client_ip"])
		assert.Equal(t, "GET", fields["method"])
		assert.Equal(t, "/events", fields["path"])
		assert.Equal(t, int64(http.StatusOK), fields["status"])
		assert.Equal(t, uint64(1), fields["request_seq"])
		assert.GreaterOrEqual(t, fields["duration_ms"], float64(openFor.Milliseconds()))
		assert.Equal(t, int64(len("data: 0\ndata: 1\n")), fields["bytes_out"])
		assert.NotContains(t, fields, "short_circuit")
	})
}

func TestStreamCutShortIsAccessLoggedWithTheBytesForwarded(t *testing.T) {
	const forwardedFrame = "data: 0\n"

	t.Run("idle timeout", func(t *testing.T) {
		const idleTimeout = 150 * time.Millisecond
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeAccessLog(t)
			backendAddr, _ := newOpenEventStreamBackend(t)
			p := newStreamingProxyClient(backendAddr)
			p.streamIdleTimeout = idleTimeout

			res := getWithin(t, startFrontend(t, p))
			_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
			require.Error(t, err)

			fields := requireAccessLoggedOnce(t, observed, backendAddr)
			assert.Equal(t, int64(http.StatusOK), fields["status"])
			assert.GreaterOrEqual(t, fields["duration_ms"], float64(idleTimeout.Milliseconds()))
			assert.Equal(t, int64(len(forwardedFrame)), fields["bytes_out"])
		})
	})

	t.Run("client disconnected", func(t *testing.T) {
		const connectedFor = 150 * time.Millisecond
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeAccessLog(t)
			backendAddr, _ := newOpenEventStreamBackend(t)

			res, disconnect := openStreamWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
			assert.Equal(t, forwardedFrame, readFrameWithin(t, bufio.NewReader(res.Body)))
			time.Sleep(connectedFor)
			disconnect()

			fields := requireAccessLoggedOnce(t, observed, backendAddr)
			assert.Equal(t, int64(http.StatusOK), fields["status"])
			assert.GreaterOrEqual(t, fields["duration_ms"], float64(connectedFor.Milliseconds()))
			assert.Equal(t, int64(len(forwardedFrame)), fields["bytes_out"])
		})
	})

	t.Run("client stopped reading", func(t *testing.T) {
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeAccessLog(t)
			backendAddr, _ := newFloodingBackend(t)
			p := newStreamingProxyClient(backendAddr)
			p.streamIdleTimeout = 300 * time.Millisecond

			res := getWithin(t, startFrontend(t, p))
			fields := requireAccessLoggedOnce(t, observed, backendAddr)
			received, _ := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })

			assert.Positive(t, fields["bytes_out"])
			assert.LessOrEqual(t, fields["bytes_out"], int64(len(received)), "the chunk whose write timed out never reached the client")
		})
	})
}

// A Middleware replacing a stream answers the request itself: one line, for
// the Short-circuit, and none for the Backend stream it closed.
func TestStreamReplacedByMiddlewareIsAccessLoggedOnce(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		observed := observeAccessLog(t)
		backendAddr, backendStreamClosed := newOpenEventStreamBackend(t)
		mw := &mockMiddleware{onResponseFunc: func(ctx *middleware.Context, err error) error {
			ctx.Response.SetStatusCode(http.StatusTeapot)
			ctx.Response.SetBodyString("crafted")
			return middleware.ErrShortCircuit
		}}

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr, mw)))
		_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		require.NoError(t, err)
		requireClosedWithin(t, backendStreamClosed, "the replaced Backend stream")

		fields := requireAccessLoggedOnce(t, observed, backendAddr)
		assert.Equal(t, true, fields["short_circuit"])
		assert.Equal(t, int64(http.StatusTeapot), fields["status"])
		assert.Equal(t, int64(len("crafted")), fields["bytes_out"])
	})
}
