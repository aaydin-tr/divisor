package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/monitoring"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/internal/server"
	cfg "github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/valyala/fasthttp/reuseport"
	"go.uber.org/zap"
)

func main() {
	configFile := flag.String("config", "./config.yaml", "config file, please use absolute path")
	flag.Parse()

	logger.InitDefaultLogger()

	// Startup failures must exit non-zero so orchestrators (compose restart
	// policies, k8s CrashLoopBackOff) notice; zap's Fatal exits 1 after
	// syncing the log entry.
	if *configFile == "" {
		zap.S().Fatal("Please provide a config file")
	}

	_, err := os.Stat(*configFile)
	if os.IsNotExist(err) {
		zap.S().Fatalf("This config file does not exist %s", *configFile)
	}

	config, err := cfg.ParseConfigFile(*configFile)
	if err != nil {
		zap.S().Fatal(err)
	}
	zap.S().Info("Parsing config file")
	err = config.PrepareConfig()
	if err != nil {
		zap.S().Fatal(err)
	}
	logger.InitLogger(config.Logging)
	zap.S().Info("Config file parsed successfully")

	middlewareExecutor, err := middleware.NewExecutor(config.Middlewares)
	if err != nil {
		zap.S().Fatal(err)
	}

	zap.S().Info("Proxies are being prepared.")
	proxies := core.NewBalancer(config, middlewareExecutor, proxy.NewProxyClient)

	if proxies == nil {
		zap.S().Fatal("No available servers")
	}
	zap.S().Infof("All proxies are ready, divisor will use `%s` algorithm health checker func will trigger every %v", config.Type, config.HealthCheckerTime)

	ln, err := reuseport.Listen("tcp4", config.GetAddr())
	if err != nil {
		zap.S().Fatalf("Error while starting divisor server %s", err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	srv, serveErr, err := server.Start(config, proxies, ln)
	if err != nil {
		zap.S().Fatalf("Error while starting divisor server %s", err)
	}

	monitoringServer, err := monitoring.Start(srv, proxies, config.GetMonitoringAddr())
	if err != nil {
		zap.S().Fatalf("Error while starting monitoring server %s", err)
	}

	select {
	case <-shutdown:
		zap.S().Info("Shutdown signal received, initiating graceful shutdown...")
	case err := <-serveErr:
		// A dead listener is a startup failure the process must not outlive.
		zap.S().Fatalf("Divisor server stopped serving: %s", err)
	}

	if err := performGracefulShutdown(srv, monitoringServer, proxies); err != nil {
		zap.S().Errorf("Error during graceful shutdown: %s", err)
		os.Exit(1)
	}

	zap.S().Info("Divisor server shutdown completed successfully")
}

// Order matters: the monitoring server polls balancer.Stats(), so it stops
// before the balancer does.
func performGracefulShutdown(srv server.Server, monitoringServer *monitoring.Server, balancer types.IBalancer) error {
	const timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	shutdownComplete := make(chan error, 1)

	go func() {
		zap.S().Info("Shutting down HTTP server...")
		if err := srv.Shutdown(ctx); err != nil {
			shutdownComplete <- err
			return
		}
		zap.S().Info("HTTP server shutdown completed")

		zap.S().Info("Shutting down monitoring server...")
		if err := monitoringServer.Shutdown(ctx); err != nil {
			shutdownComplete <- err
			return
		}
		zap.S().Info("Monitoring server shutdown completed")

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
