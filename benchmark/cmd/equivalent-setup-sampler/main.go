// Command equivalent-setup-sampler prints, as JSON, the Benchmark backend headers of every answer it
// gets through a Contender. It runs in a container on the host network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/aaydin-tr/divisor/benchmark/equivalentsetup"
	"github.com/aaydin-tr/divisor/benchmark/plan"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/", "the Contender to send the requests through")
	requests := flag.Int("requests", 0, "how many requests to send")
	connections := flag.Int("connections", 0, "how many keep-alive connections to send them over")
	flag.Parse()
	if *requests < 1 || *connections < 1 {
		log.Fatal("--requests and --connections must be at least 1")
	}

	sample, err := equivalentsetup.Collect(context.Background(), *url, plan.EquivalentSetupCheck{Requests: *requests, Connections: *connections})
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(sample); err != nil {
		log.Fatal(err)
	}
}
