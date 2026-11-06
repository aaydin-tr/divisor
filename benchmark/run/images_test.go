package run

import (
	"errors"
	"testing"
)

func TestAContenderImageThatCannotBeBuiltFailsWithDockersError(t *testing.T) {
	dockerBuildError := errors.New(`docker build --quiet --tag divisor-benchmark/contender-divisor --build-arg DIVISOR_IMAGE=ghcr.io/aaydin-tr/divisor:v9.9.9 contenders/divisor: exit status 1: Dockerfile:4
--------------------
   3 |     ARG DIVISOR_IMAGE
   4 | >>> FROM ${DIVISOR_IMAGE}
--------------------
ERROR: failed to build: failed to solve: ghcr.io/aaydin-tr/divisor:v9.9.9: failed to resolve source metadata for ghcr.io/aaydin-tr/divisor:v9.9.9: ghcr.io/aaydin-tr/divisor:v9.9.9: not found

View build details: docker-desktop://dashboard/build/desktop-linux/desktop-linux/u2jhiz2abtssk3dt7korxa772`)

	got := unbuiltImageFailure("ghcr.io/aaydin-tr/divisor:v9.9.9", dockerBuildError)

	want := "image ghcr.io/aaydin-tr/divisor:v9.9.9 not built: failed to build: failed to solve: ghcr.io/aaydin-tr/divisor:v9.9.9: failed to resolve source metadata for ghcr.io/aaydin-tr/divisor:v9.9.9: ghcr.io/aaydin-tr/divisor:v9.9.9: not found"
	if got != want {
		t.Errorf("failure = %q\nwant %q", got, want)
	}
}
