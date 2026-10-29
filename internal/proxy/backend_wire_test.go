package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// newWireRecordingProxyClient returns a ProxyClient whose Backend is an
// in-memory connection: each request divisor writes is read in full, as a
// real Backend would, answered 200, and its raw bytes sent on the channel.
func newWireRecordingProxyClient(middlewares ...middleware.Middleware) (*ProxyClient, <-chan string) {
	backendWire := make(chan string, 1)
	p := createTestProxyWithMiddlewares(config.Backend{Url: "backend:8080"}, nil, middlewares...)
	p.proxy.Dial = func(string) (net.Conn, error) {
		divisorSide, backendSide := net.Pipe()
		go answerAndRecordOneRequest(backendSide, backendWire)
		return divisorSide, nil
	}
	return p, backendWire
}

func answerAndRecordOneRequest(conn net.Conn, backendWire chan<- string) {
	defer conn.Close()
	var wire bytes.Buffer
	req, err := http.ReadRequest(bufio.NewReader(io.TeeReader(conn, &wire)))
	if err == nil {
		_, err = io.Copy(io.Discard, req.Body)
	}
	if err != nil {
		backendWire <- fmt.Sprintf("unreadable request (%v): %q", err, wire.String())
		return
	}
	conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")) //nolint:errcheck
	backendWire <- wire.String()
}

// decodedBody returns a recorded request's body with its framing removed.
func decodedBody(t *testing.T, wire string) string {
	t.Helper()
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(wire)))
	require.NoError(t, err)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return string(body)
}

// frontends turn raw client bytes into the RequestCtx the balancer sees, the
// way each HTTP/1.1 listener does, and proxy it through p.
var frontends = []struct {
	name  string
	proxy func(t *testing.T, p *ProxyClient, rawRequest string)
}{
	{"fasthttp", proxyThroughFasthttpFrontend},
	{"net/http", proxyThroughNetHttpFrontend},
}

func proxyThroughFasthttpFrontend(t *testing.T, p *ProxyClient, rawRequest string) {
	t.Helper()
	var ctx fasthttp.RequestCtx
	require.NoError(t, ctx.Request.Read(bufio.NewReader(strings.NewReader(rawRequest))))
	require.NoError(t, p.ReverseProxyHandler(&ctx))
}

func proxyThroughNetHttpFrontend(t *testing.T, p *ProxyClient, rawRequest string) {
	t.Helper()
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(rawRequest)))
	require.NoError(t, err)
	var proxyErr error
	NewNetHttpAdapter(&stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
		proxyErr = p.ReverseProxyHandler(ctx)
	}}, 0).ServeHTTP(httptest.NewRecorder(), req)
	require.NoError(t, proxyErr)
}

func reverseProxyThrough(p *ProxyClient) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) { _ = p.ReverseProxyHandler(ctx) }
}

// newHTTP2Frontend serves the adapter over TLS and HTTP/2, like the net/http
// listener, proxying every request through p.
func newHTTP2Frontend(t *testing.T, p *ProxyClient) *httptest.Server {
	t.Helper()
	return newHTTP2FrontendWithWriteTimeout(t, p, noWriteTimeout)
}

func newHTTP2FrontendWithWriteTimeout(t *testing.T, p *ProxyClient, writeTimeout time.Duration) *httptest.Server {
	t.Helper()
	return newHTTP2FrontendServing(t, reverseProxyThrough(p), writeTimeout)
}

func newHTTP2FrontendServing(t *testing.T, serve fasthttp.RequestHandler, writeTimeout time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(NewNetHttpAdapter(&stubBalancer{handler: serve}, 0))
	server.Config.WriteTimeout = writeTimeout
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
