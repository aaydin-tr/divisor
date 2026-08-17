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

// specRed marks a test that asserts agreed 1.0 spec behavior that has not
// shipped yet (born red, see docs/adr/0002-spec-red-gating.md). Such tests
// are skipped unless DIVISOR_INTEGRATION_SPEC_RED=1, so the blocking
// Integration CI job stays green while an advisory job tracks the spec gap.
// When the behavior ships, remove the specRed call so the test gates PRs.
func specRed(t *testing.T, todoItem string) {
	t.Helper()
	switch os.Getenv("DIVISOR_INTEGRATION_SPEC_RED") {
	case "", "0", "false":
		t.Skipf("born-red 1.0 spec test (%s); set DIVISOR_INTEGRATION_SPEC_RED=1 to run it", todoItem)
	}
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

	// DIVISOR_IT_PREBUILT=1 says the two suite images were built outside the
	// test binary (CI does this with buildx + GHA layer caching); only verify
	// they exist so a missing image fails loudly instead of as a cryptic
	// container-start error.
	if os.Getenv("DIVISOR_IT_PREBUILT") == "1" {
		for _, img := range []string{divisorImage, echoImage} {
			if _, err := pool.Client.InspectImage(img + ":" + imageTag); err != nil {
				fmt.Printf("DIVISOR_IT_PREBUILT is set but image %s:%s is not present: %v\n", img, imageTag, err)
				return 1
			}
		}
	} else {
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
