package main

import (
	"fmt"
	"net"
	"time"

	balancer "github.com/aaydin-tr/balancer/core"
	"github.com/aaydin-tr/balancer/internal/monitoring"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/pkg/http"
	"github.com/aaydin-tr/balancer/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

func main() {
	logger.InitLogger()

	config := config.ParseConfigFile("./config.yaml")
	config.PrepareConfig()

	zap.S().Infof("Proxies are being prepared.")
	proxies := balancer.NewBalancer(config, http.NewHttpClient().DefaultHealtChecker, config.HealtCheckerTime, helper.HashFunc)

	if proxies == nil {
		fmt.Println("No avaible serves")
		return
	}
	zap.S().Infof("All proxies are ready, divisor will use `%s` algorithm healt checker func will triger every %v", config.Type, config.HealtCheckerTime)

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

	zap.S().Infof("divisor server started successfully -> %s", config.GetAddr())
	server.Serve(ln)
}
