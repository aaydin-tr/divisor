package run

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

const (
	benchmarkBackendImage = "divisor-benchmark/backend"
	loadGeneratorImage    = "divisor-benchmark/load-generator"

	contenderContainer     = "divisor-benchmark-contender"
	loadGeneratorContainer = "divisor-benchmark-load-generator"
)

func contenderImage(contender *plan.Contender) string {
	return "divisor-benchmark/contender-" + contender.ID
}

func benchmarkBackendContainer(benchmarkBackend plan.BenchmarkBackend) string {
	return "divisor-benchmark-" + benchmarkBackend.Name
}

type docker struct{}

// output runs the docker CLI and returns its stdout; a failure carries stderr.
func (d docker) output(ctx context.Context, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", arguments...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil {
		return stdout, fmt.Errorf("docker %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout, nil
}

func (d docker) combinedOutput(ctx context.Context, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", arguments...).CombinedOutput()
}

// removeContainers runs on its own context: it also cleans up after an
// interrupted run, whose context is already canceled.
func (d docker) removeContainers(names ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _ = d.combinedOutput(ctx, append([]string{"rm", "--force"}, names...)...)
}
