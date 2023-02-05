package main

import (
	"fmt"
	"log"
	"time"

	balancer "github.com/aaydin-tr/balancer/core"
	"github.com/aaydin-tr/balancer/internal/monitoring"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/pkg/http"
	"github.com/valyala/fasthttp"
)

func main() {
	config := config.ParseConfigFile("./config.yaml")
	config.PrepareConfig()

	proxies := balancer.NewBalancer(config, http.NewHttpClient().DefaultHealtChecker, 5*time.Second, helper.HashFunc)

	if proxies == nil {
		fmt.Println("No avaible serves")
		return
	}

	server := fasthttp.Server{
		Handler:               proxies.Serve(),
		MaxIdleWorkerDuration: 5 * time.Second,
		TCPKeepalivePeriod:    5 * time.Second,
		TCPKeepalive:          true,
	}

	go monitoring.StartMonitoringServer(&server, proxies, config.GetMonitoringAddr())

	if err := server.ListenAndServe(config.GetAddr()); err != nil {
		log.Fatalf("error in fasthttp server: %s", err)
	}

}
