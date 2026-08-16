// Package integration is the black-box integration suite for divisor.
//
// Divisor and its Backends each run as Docker containers (see
// docs/adr/0001-black-box-integration-suite.md); tests drive real HTTP
// traffic from outside. The suite is opt-in: set DIVISOR_INTEGRATION=1.
//
//	DIVISOR_INTEGRATION=1 go test -v -timeout 20m .
package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
)

var (
	pool    *dockertest.Pool
	network *dockertest.Network
)

func TestMain(m *testing.M) {
	switch os.Getenv("DIVISOR_INTEGRATION") {
	case "", "0", "false":
		fmt.Println("integration suite skipped: set DIVISOR_INTEGRATION=1 to run it (requires a Docker daemon)")
		return
	}
	os.Exit(run(m))
}

func run(m *testing.M) int {
	var err error
	pool, err = dockertest.NewPool("")
	if err != nil {
		fmt.Printf("DIVISOR_INTEGRATION is set but the Docker daemon is not reachable: %v\n", err)
		return 1
	}
	if err := pool.Client.Ping(); err != nil {
		fmt.Printf("DIVISOR_INTEGRATION is set but the Docker daemon is not reachable: %v\n", err)
		return 1
	}
	pool.MaxWait = 2 * time.Minute

	removeStaleContainers()

	fmt.Println("building divisor image...")
	if err := buildImage(divisorImage, "..", "Dockerfile"); err != nil {
		fmt.Printf("building divisor image: %v\n", err)
		return 1
	}
	fmt.Println("building echo backend image...")
	if err := buildImage(echoImage, "echobackend", "Dockerfile"); err != nil {
		fmt.Printf("building echo backend image: %v\n", err)
		return 1
	}

	removeStaleNetworks()
	network, err = pool.CreateNetwork(networkName)
	if err != nil {
		fmt.Printf("creating docker network: %v\n", err)
		return 1
	}
	defer network.Close()

	return m.Run()
}
