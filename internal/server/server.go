// Package server runs the client-facing listener on whichever stack the
// config selects: fasthttp for HTTP/1.1, net/http + x/net/http2 for HTTP/2.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

// Server is a running client-facing listener, stack-agnostic.
type Server interface {
	Shutdown(ctx context.Context) error
	OpenConnectionsCount() int32
}

// Start serves ln in the background. The returned channel delivers the error
// that ended serving, if any; a clean Shutdown delivers nothing. A nil error
// return guarantees a non-nil Server.
func Start(cfg *config.Config, balancer types.IBalancer, ln net.Listener) (Server, <-chan error, error) {
	ln = withTCPKeepalive(ln, cfg.Server.TCPKeepalivePeriod)
	if cfg.Server.HttpVersion == config.Http2 {
		zap.S().Info("Starting net/http server with HTTP/2")
		return startNetHttp(cfg, balancer, ln)
	}
	zap.S().Info("Starting fasthttp server with HTTP/1.1")
	return startFasthttp(cfg, balancer, ln)
}

type fasthttpServer struct{ *fasthttp.Server }

func (s fasthttpServer) Shutdown(ctx context.Context) error { return s.ShutdownWithContext(ctx) }
func (s fasthttpServer) OpenConnectionsCount() int32        { return s.GetOpenConnectionsCount() }

func startFasthttp(cfg *config.Config, balancer types.IBalancer, ln net.Listener) (Server, <-chan error, error) {
	srv := &fasthttp.Server{
		Handler:               balancer.Serve(),
		MaxIdleWorkerDuration: cfg.Server.MaxIdleWorkerDuration,
		Concurrency:           cfg.Server.Concurrency,
		ReadTimeout:           cfg.Server.ReadTimeout,
		WriteTimeout:          cfg.Server.WriteTimeout,
		IdleTimeout:           cfg.Server.IdleTimeout,
		DisableKeepalive:      cfg.Server.DisableKeepalive,
		MaxRequestBodySize:    cfg.Server.MaxRequestBodySize,
		ReadBufferSize:        cfg.Server.ReadBufferSize,
		WriteBufferSize:       cfg.Server.WriteBufferSize,
		MaxConnsPerIP:         cfg.Server.MaxConnsPerIP,
		MaxRequestsPerConn:    cfg.Server.MaxRequestsPerConn,
		TLSConfig:             tlsConfig(cfg),
		ErrorHandler:          proxy.ErrorHandler,
		Name:                  "divisor",
	}

	// fasthttp returns nil from Serve once Shutdown closes the listener.
	serve := func() error { return srv.Serve(ln) }
	if cfg.Server.CertFile != "" && cfg.Server.KeyFile != "" {
		serve = func() error { return srv.ServeTLS(ln, cfg.Server.CertFile, cfg.Server.KeyFile) }
	}

	return fasthttpServer{srv}, serveInBackground(cfg, serve), nil
}

type netHttpServer struct{ *http.Server }

func (s netHttpServer) Shutdown(ctx context.Context) error { return s.Server.Shutdown(ctx) }

// TODO: Implement net/http connection count (ConnState hook); net/http does
// not expose one.
func (s netHttpServer) OpenConnectionsCount() int32 { return 0 }

func startNetHttp(cfg *config.Config, balancer types.IBalancer, ln net.Listener) (Server, <-chan error, error) {
	srv := &http.Server{
		Addr:         cfg.GetAddr(),
		Handler:      proxy.NewNetHttpAdapter(balancer, cfg.Server.MaxRequestBodySize),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
		TLSConfig:    tlsConfig(cfg),
	}
	srv.SetKeepAlivesEnabled(!cfg.Server.DisableKeepalive)

	if err := http2.ConfigureServer(srv, &http2.Server{}); err != nil {
		return nil, nil, fmt.Errorf("configuring HTTP/2 server: %w", err)
	}

	serve := func() error {
		err := srv.ServeTLS(ln, cfg.Server.CertFile, cfg.Server.KeyFile)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return netHttpServer{srv}, serveInBackground(cfg, serve), nil
}

// tlsConfig carries server.tls_min_version onto whichever stack serves TLS;
// both ServeTLS paths load the certificate pair into this config themselves.
// Nil when unset, so the runtime default stays untouched.
func tlsConfig(cfg *config.Config) *tls.Config {
	minVersion := cfg.Server.TLSMinVersionValue()
	if minVersion == 0 {
		return nil
	}
	return &tls.Config{MinVersion: minVersion}
}

func serveInBackground(cfg *config.Config, serve func() error) <-chan error {
	errc := make(chan error, 1)
	go func() {
		zap.S().Infof("Divisor server is running on %s", cfg.GetURL())
		if err := serve(); err != nil {
			errc <- err
		}
	}()
	return errc
}
