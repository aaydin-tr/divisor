package server

import (
	"context"
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
