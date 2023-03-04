package main

import (
	"flag"
	"net"
	"time"

	balancer "github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/internal/monitoring"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

func main() {
	configFile := flag.String("config", "./config.yaml", "config file, please use absolute path")
	flag.Parse()

	logFile := helper.GetLogFile()
	logger.InitLogger(logFile)

	config, err := config.ParseConfigFile(*configFile)
	if err != nil {
		zap.S().Error(err)
		return
	}
	config.PrepareConfig()

	zap.S().Infof("Proxies are being prepared.")
	proxies := balancer.NewBalancer(config, proxy.NewProxyClient)

	if proxies == nil {
		zap.S().Error("No avaible serves")
		return
	}
	zap.S().Infof("All proxies are ready, divisor will use `%s` algorithm healt checker func will triger every %v", config.Type, config.HealthCheckerTime)

	server := fasthttp.Server{
		// TODO must be editable by config
		Handler:               proxies.Serve(),
		MaxIdleWorkerDuration: 5 * time.Second,
		TCPKeepalivePeriod:    5 * time.Second,
		TCPKeepalive:          true,
		NoDefaultServerHeader: true,
	}

	go monitoring.StartMonitoringServer(&server, proxies, config.GetMonitoringAddr())

	ln, err := net.Listen("tcp4", config.GetAddr())
	if err != nil {
		zap.S().Errorf("Error while starting divisor server %s", err)
		return
	}

	zap.S().Infof("divisor server started successfully -> http://%s", config.GetAddr())
	server.Serve(ln)
}
