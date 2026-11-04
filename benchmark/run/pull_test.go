package run

import (
	"errors"
	"testing"
)

func TestAContenderImageThatCannotBePulledFailsWithDockersError(t *testing.T) {
	dockerPullError := errors.New(`docker pull --quiet ghcr.io/aaydin-tr/divisor:v0.6.0: exit status 1: Error response from daemon: failed to resolve reference "ghcr.io/aaydin-tr/divisor:v0.6.0": ghcr.io/aaydin-tr/divisor:v0.6.0: not found`)

	got := unpulledImageFailure("ghcr.io/aaydin-tr/divisor:v0.6.0", dockerPullError)

	want := `image ghcr.io/aaydin-tr/divisor:v0.6.0 not pulled: failed to resolve reference "ghcr.io/aaydin-tr/divisor:v0.6.0": ghcr.io/aaydin-tr/divisor:v0.6.0: not found`
	if got != want {
		t.Errorf("failure = %q\nwant %q", got, want)
	}
}
