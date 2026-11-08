package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/internal/testcert"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// brokenListener fails every Accept permanently, the way a listener whose
// socket was torn down under the server does.
type brokenListener struct {
	net.Listener
	err error
}

func (l brokenListener) Accept() (net.Conn, error) { return nil, l.err }

func newConfig(t *testing.T, httpVersion string) *config.Config {
	t.Helper()
	cfg := &config.Config{Host: "127.0.0.1", Port: "0", Server: config.Server{HttpVersion: httpVersion}}
	if httpVersion == config.Http2 {
		addTestCertificate(t, cfg)
	}
	return cfg
}

func addTestCertificate(t *testing.T, cfg *config.Config) {
	t.Helper()
	certPath, keyPath, err := testcert.Write(t.TempDir())
	assert.NoError(t, err)
	cfg.Server.CertFile, cfg.Server.KeyFile = certPath, keyPath
}

func newFasthttpConfig(t *testing.T) *config.Config {
	t.Helper()
	return newConfig(t, config.Http1)
}

func newFasthttpOverTLSConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := newConfig(t, config.Http1)
	addTestCertificate(t, cfg)
	return cfg
}

func newNetHttpConfig(t *testing.T) *config.Config {
	t.Helper()
	return newConfig(t, config.Http2)
}

func enableHTTP1(protocols *http.Protocols) { protocols.SetHTTP1(true) }

func enableHTTP2(protocols *http.Protocols) { protocols.SetHTTP2(true) }

func newClientSpeaking(enableProtocol func(*http.Protocols)) *http.Client {
	var protocols http.Protocols
	enableProtocol(&protocols)
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Protocols:       &protocols,
	}}
}

func localListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	return ln
}

func TestStartReturnsNonNilServerForBothStacks(t *testing.T) {
	for _, version := range []string{config.Http1, config.Http2} {
		t.Run(version, func(t *testing.T) {
			ln := localListener(t)
			srv, errc, err := Start(newConfig(t, version), &mocks.MockBalancer{}, ln)
			assert.NoError(t, err)
			assert.NotNil(t, srv)
			assert.NotNil(t, errc)
			assert.NoError(t, srv.Shutdown(context.Background()))
		})
	}
}

// The configured knobs must reach the constructed fasthttp server: a value
// validated in config but never wired would silently run on library defaults.
func TestConfiguredKnobsReachTheFasthttpServer(t *testing.T) {
	cfg := newConfig(t, config.Http1)
	cfg.Server.ReadBufferSize = 16384
	cfg.Server.WriteBufferSize = 8192
	cfg.Server.MaxConnsPerIP = 100
	cfg.Server.MaxRequestsPerConn = 1000

	ln := localListener(t)
	srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
	assert.NoError(t, err)
	defer srv.Shutdown(context.Background())

	inner := srv.(fasthttpServer).Server
	assert.Equal(t, 16384, inner.ReadBufferSize)
	assert.Equal(t, 8192, inner.WriteBufferSize)
	assert.Equal(t, 100, inner.MaxConnsPerIP)
	assert.Equal(t, 1000, inner.MaxRequestsPerConn)
}

func TestHTTP2ConfigReachesTheNetHttpServer(t *testing.T) {
	cfg := newConfig(t, config.Http2)
	cfg.Server.HTTP2.MaxConcurrentStreams = 500

	ln := localListener(t)
	srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
	require.NoError(t, err)
	defer srv.Shutdown(context.Background())

	inner := srv.(netHttpServer).Server
	require.NotNil(t, inner.HTTP2)
	assert.Equal(t, 500, inner.HTTP2.MaxConcurrentStreams)
}

func TestTLSMinVersionReachesBothStacks(t *testing.T) {
	t.Run("fasthttp", func(t *testing.T) {
		cfg := newConfig(t, config.Http1)
		cfg.Server.TLSMinVersion = config.TLSMinVersion13

		ln := localListener(t)
		srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
		assert.NoError(t, err)
		defer srv.Shutdown(context.Background())

		inner := srv.(fasthttpServer).Server
		assert.NotNil(t, inner.TLSConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), inner.TLSConfig.MinVersion)
	})

	t.Run("net/http", func(t *testing.T) {
		cfg := newConfig(t, config.Http2)
		cfg.Server.TLSMinVersion = config.TLSMinVersion13

		ln := localListener(t)
		srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
		assert.NoError(t, err)
		defer srv.Shutdown(context.Background())

		inner := srv.(netHttpServer).Server
		assert.NotNil(t, inner.TLSConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), inner.TLSConfig.MinVersion)
	})
}

func TestNetHttpStackServesHTTP2AndHTTP1(t *testing.T) {
	ln := localListener(t)
	srv, _, err := Start(newConfig(t, config.Http2), &mocks.MockBalancer{}, ln)
	require.NoError(t, err)
	defer srv.Shutdown(context.Background())

	for _, tc := range []struct {
		name           string
		enableProtocol func(*http.Protocols)
		wantProtoMajor int
	}{
		{"HTTP/2 client", enableHTTP2, 2},
		{"HTTP/1.1 client", enableHTTP1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := newClientSpeaking(tc.enableProtocol).Get("https://" + ln.Addr().String())
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, tc.wantProtoMajor, res.ProtoMajor)
		})
	}
}

func TestUnsetTLSMinVersionLeavesTheRuntimeDefault(t *testing.T) {
	cfg := newConfig(t, config.Http1)
	ln := localListener(t)
	srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
	assert.NoError(t, err)
	defer srv.Shutdown(context.Background())

	assert.Nil(t, srv.(fasthttpServer).Server.TLSConfig)
}

func TestServeFailureIsReported(t *testing.T) {
	boom := errors.New("accept: socket gone")
	ln := brokenListener{Listener: localListener(t), err: boom}

	_, errc, err := Start(newConfig(t, config.Http1), &mocks.MockBalancer{}, ln)
	assert.NoError(t, err)

	select {
	case got := <-errc:
		assert.ErrorIs(t, got, boom)
	case <-time.After(5 * time.Second):
		t.Fatal("a permanent Accept failure was never reported")
	}
}

func TestCleanShutdownReportsNothing(t *testing.T) {
	ln := localListener(t)
	srv, errc, err := Start(newConfig(t, config.Http1), &mocks.MockBalancer{}, ln)
	assert.NoError(t, err)
	assert.NoError(t, srv.Shutdown(context.Background()))

	select {
	case got := <-errc:
		t.Fatalf("clean shutdown reported an error: %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

// heldResponseBalancer answers a request only once the test releases it, so
// its response can be written while Shutdown runs.
type heldResponseBalancer struct {
	mocks.MockBalancer
	requestArrived  chan struct{}
	releaseResponse chan struct{}
}

func newHeldResponseBalancer() *heldResponseBalancer {
	return &heldResponseBalancer{requestArrived: make(chan struct{}), releaseResponse: make(chan struct{})}
}

func (b *heldResponseBalancer) Serve() func(ctx *fasthttp.RequestCtx) {
	return func(ctx *fasthttp.RequestCtx) {
		close(b.requestArrived)
		<-b.releaseResponse
		ctx.SetStatusCode(fasthttp.StatusOK)
	}
}

// Both stacks mark themselves shutting down before they close their listener.
type closeReportingListener struct {
	net.Listener
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *closeReportingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

type receivedResponse struct {
	statusCode      int
	connectionClose bool
	err             error
}

func getOverHTTP1(url string) receivedResponse {
	res, err := newClientSpeaking(enableHTTP1).Get(url)
	if err != nil {
		return receivedResponse{err: err}
	}
	defer res.Body.Close()
	_, err = io.Copy(io.Discard, res.Body)
	// Go moves Connection: close out of Header and into Close.
	return receivedResponse{statusCode: res.StatusCode, connectionClose: res.Close, err: err}
}

func receiveWithin[T any](t *testing.T, ch <-chan T, awaited string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", awaited)
		var zero T
		return zero
	}
}

// A client that reuses the connection after such a response would find it closed; Connection: close sends it to
// open a new one instead.
func TestResponseWrittenDuringShutdownClosesTheConnection(t *testing.T) {
	for _, stack := range []struct {
		name      string
		newConfig func(t *testing.T) *config.Config
		scheme    string
	}{
		{"fasthttp", newFasthttpConfig, "http"},
		{"fasthttp over TLS", newFasthttpOverTLSConfig, "https"},
		{"net/http", newNetHttpConfig, "https"},
	} {
		t.Run(stack.name, func(t *testing.T) {
			balancer := newHeldResponseBalancer()
			ln := &closeReportingListener{Listener: localListener(t), closed: make(chan struct{})}
			srv, _, err := Start(stack.newConfig(t), balancer, ln)
			require.NoError(t, err)

			response := make(chan receivedResponse, 1)
			go func() { response <- getOverHTTP1(stack.scheme + "://" + ln.Addr().String()) }()
			receiveWithin(t, balancer.requestArrived, "the request to reach the balancer")

			shutdownResult := make(chan error, 1)
			go func() { shutdownResult <- srv.Shutdown(context.Background()) }()
			receiveWithin(t, ln.closed, "Shutdown to close the listener")
			close(balancer.releaseResponse)

			got := receiveWithin(t, response, "the response")
			require.NoError(t, got.err)
			assert.Equal(t, http.StatusOK, got.statusCode)
			assert.True(t, got.connectionClose, "a response written during shutdown must carry Connection: close")
			assert.NoError(t, receiveWithin(t, shutdownResult, "Shutdown to return"))
		})
	}
}
