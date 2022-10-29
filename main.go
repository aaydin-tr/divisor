package main

import (
	"log"

	balancer "github.com/aaydin-tr/balancer/core"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/valyala/fasthttp"
)

func main() {
	configFile := config.ParseConfigFile("./config.yaml")
	config := config.PrepareConfig(configFile)

	proxies := balancer.NewBalancer(config)

	if err := fasthttp.ListenAndServe(":8000", proxies.Serve()); err != nil {
		log.Fatalf("error in fasthttp server: %s", err)
	}
}
