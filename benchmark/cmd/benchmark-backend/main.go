package main

import (
	"flag"
	"log"

	"github.com/aaydin-tr/divisor/benchmark/backend"
)

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:9001", "host:port to serve on")
	backendName := flag.String("name", "backend-1", "the name answers carry in "+backend.AnsweredByHeader)
	flag.Parse()

	if err := backend.NewServer(*backendName).ListenAndServe(*listenAddress); err != nil {
		log.Fatal(err)
	}
}
