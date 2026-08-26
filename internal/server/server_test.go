package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/internal/testcert"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
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
		certPath, keyPath, err := testcert.Write(t.TempDir())
		assert.NoError(t, err)
		cfg.Server.CertFile, cfg.Server.KeyFile = certPath, keyPath
	}
	return cfg
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
