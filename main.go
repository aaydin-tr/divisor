package main

import (
	"flag"
	"net"
	"os"

	balancer "github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/internal/monitoring"
	"github.com/aaydin-tr/divisor/internal/proxy"
	cfg "github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/logger"
	http2 "github.com/diamondcdn/fasthttp-http2"
	"github.com/valyala/fasthttp"
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
	zap.S().Info("Config file parse successfully")

	zap.S().Info("Proxies are being prepared.")
	proxies := balancer.NewBalancer(config, proxy.NewProxyClient)

	if proxies == nil {
		zap.S().Error("No avaible serves")
		return
	}
	zap.S().Infof("All proxies are ready, divisor will use `%s` algorithm healt checker func will triger every %v", config.Type, config.HealthCheckerTime)

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
		NoDefaultServerHeader:         true,
	}

	go monitoring.StartMonitoringServer(&server, proxies, config.GetMonitoringAddr())

	ln, err := net.Listen("tcp4", config.GetAddr())
	if err != nil {
		zap.S().Errorf("Error while starting divisor server %s", err)
		return
	}

	if config.Server.HttpVersion == cfg.Http2 {
		http2.ConfigureServer(&server, http2.ServerConfig{})
	}

	if config.Server.CertFile != "" && config.Server.KeyFile != "" {
		zap.S().Infof("divisor server started successfully -> https://%s", config.GetAddr())
		server.ServeTLS(ln, config.Server.CertFile, config.Server.KeyFile)
	}

	zap.S().Infof("divisor server started successfully -> http://%s", config.GetAddr())
	server.Serve(ln)
}
