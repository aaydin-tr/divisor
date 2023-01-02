package main

import (
	"fmt"
	"log"
	"time"

	balancer "github.com/aaydin-tr/balancer/core"
	"github.com/aaydin-tr/balancer/pkg/config"
	healthchecker "github.com/aaydin-tr/balancer/pkg/health-checker"
	"github.com/aaydin-tr/balancer/pkg/http"

	"github.com/valyala/fasthttp"
)

func main() {
	config := config.ParseConfigFile("./config.yaml")
	config.PrepareConfig()

	proxies := balancer.NewBalancer(config, http.NewHttpClient().DefaultHealtChecker, 5*time.Second)

	if proxies == nil {
		fmt.Println("No avaible serves")
		return
	}

	server := fasthttp.Server{
		Handler:               proxies.Serve(),
		MaxIdleWorkerDuration: 5 * time.Minute,
		TCPKeepalivePeriod:    5 * time.Minute,
		TCPKeepalive:          true,
	}
	healthchecker.HealthChecker()
	if err := server.ListenAndServe(":8000"); err != nil {
		log.Fatalf("error in fasthttp server: %s", err)
	}
}
