package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	balancer "github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/monitoring"
	"github.com/aaydin-tr/divisor/internal/proxy"
	cfg "github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/aaydin-tr/http2"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
	"go.uber.org/zap"
)

func main() {
	configFile := flag.String("config", "./config.yaml", "config file, please use absolute path")
	flag.Parse()

	logFile := helper.GetLogFile()
	logger.InitLogger(logFile)

	if *configFile == "" {
		zap.S().Error("Please provide a config file")
		return
	}

	_, err := os.Stat(*configFile)
	if os.IsNotExist(err) {
		zap.S().Errorf("This config file does not exist %s", *configFile)
		return
	}

	config, err := cfg.ParseConfigFile(*configFile)
	if err != nil {
		zap.S().Error(err)
		return
	}
	zap.S().Info("Parsing config file")
	err = config.PrepareConfig()
	if err != nil {
		zap.S().Error(err)
		return
	}
	zap.S().Info("Config file parsed successfully")

	zap.S().Info("Proxies are being prepared.")
	proxies := balancer.NewBalancer(config, proxy.NewProxyClient)

	if proxies == nil {
		zap.S().Error("No available servers")
		return
	}
	zap.S().Infof("All proxies are ready, divisor will use `%s` algorithm health checker func will trigger every %v", config.Type, config.HealthCheckerTime)

	server := fasthttp.Server{
		Handler:                       proxies.Serve(),
		MaxIdleWorkerDuration:         config.Server.MaxIdleWorkerDuration,
		TCPKeepalivePeriod:            config.Server.TCPKeepalivePeriod,
		Concurrency:                   config.Server.Concurrency,
		ReadTimeout:                   config.Server.ReadTimeout,
		WriteTimeout:                  config.Server.WriteTimeout,
		IdleTimeout:                   config.Server.IdleTimeout,
		DisableKeepalive:              config.Server.DisableKeepalive,
		DisableHeaderNamesNormalizing: config.Server.DisableHeaderNamesNormalizing,
		Name:                          "divisor",
	}

	// Start monitoring server
	go monitoring.StartMonitoringServer(&server, proxies, config.GetMonitoringAddr())

	ln, err := reuseport.Listen("tcp4", config.GetAddr())
	if err != nil {
		zap.S().Errorf("Error while starting divisor server %s", err)
		return
	}

	if config.Server.HttpVersion == cfg.Http2 {
		http2.ConfigureServer(&server, http2.ServerConfig{})
	}

	// Set up graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		zap.S().Infof("Divisor server is running on %s", config.GetURL())
		var err error
		if config.Server.CertFile != "" && config.Server.KeyFile != "" {
			err = server.ServeTLS(ln, config.Server.CertFile, config.Server.KeyFile)
		} else {
			err = server.Serve(ln)
		}

		if err != nil {
			zap.S().Errorf("Error while running divisor server %s", err)
		}
	}()

	// Wait for shutdown signal
	<-shutdown
	zap.S().Info("Shutdown signal received, initiating graceful shutdown...")

	// Perform graceful shutdown
	if err := performGracefulShutdown(&server, proxies); err != nil {
		zap.S().Errorf("Error during graceful shutdown: %s", err)
		os.Exit(1)
	}

	zap.S().Info("Divisor server shutdown completed successfully")
}

func performGracefulShutdown(server *fasthttp.Server, balancer types.IBalancer) error {
	// Create a context with 30-second timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Channel to signal shutdown completion
	shutdownComplete := make(chan error, 1)

	go func() {
		zap.S().Info("Shutting down HTTP server...")

		// Shutdown the HTTP server gracefully
		if err := server.Shutdown(); err != nil {
			shutdownComplete <- err
			return
		}

		zap.S().Info("HTTP server shutdown completed")

		// Shutdown the balancer (stop health checkers, close connections)
		zap.S().Info("Shutting down load balancer...")
		if err := balancer.Shutdown(); err != nil {
			shutdownComplete <- err
			return
		}

		zap.S().Info("Load balancer shutdown completed")
		shutdownComplete <- nil
	}()

	// Wait for either shutdown completion or timeout
	select {
	case err := <-shutdownComplete:
		if err != nil {
			return err
		}
		zap.S().Info("Graceful shutdown completed successfully")
		return nil
	case <-ctx.Done():
		zap.S().Warn("Graceful shutdown timeout reached, forcing shutdown")
		return ctx.Err()
	}
}
