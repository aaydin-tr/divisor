package run

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

const peakMemoryReaderContainer = "divisor-benchmark-peak-memory-reader"

const bytesPerMiB = 1 << 20

// memoryPeakFileCandidates are where a container's cgroup v2 memory.peak is in the host cgroup
// namespace: under Docker's systemd cgroup driver (GitHub's ubuntu-latest), then its cgroupfs driver
// (Docker Desktop).
func memoryPeakFileCandidates(containerID string) []string {
	return []string{
		path.Join("/sys/fs/cgroup/system.slice", "docker-"+containerID+".scope", "memory.peak"),
		path.Join("/sys/fs/cgroup/docker", containerID, "memory.peak"),
	}
}

// readPeakMemory reads from a helper container, since a Contender image may have no shell. No value
// is never a run error: cgroup v1 and kernels before Linux 5.19 have no memory.peak.
func (r *runner) readPeakMemory(ctx context.Context, turn *plan.Turn, contenderContainerID string) *int64 {
	var readFailures []error
	for _, memoryPeakFile := range memoryPeakFileCandidates(contenderContainerID) {
		output, err := r.docker.output(ctx, "run", "--rm", "--name", peakMemoryReaderContainer, "--network", "none",
			"--cpuset-cpus", r.plan.Resources.LoadGeneratorCPUs, "--privileged", "--cgroupns=host",
			"--entrypoint", "cat", benchmarkBackendImage, memoryPeakFile)
		if err != nil {
			readFailures = append(readFailures, err)
			continue
		}
		peakMemoryBytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
		if err != nil {
			readFailures = append(readFailures, err)
			continue
		}
		r.logTurnf(turn, "%s: peak memory %.1f MiB", turn.Contender.Name, float64(peakMemoryBytes)/bytesPerMiB)
		return &peakMemoryBytes
	}
	r.logTurnf(turn, "%s: peak memory not read, it shows as n/a: %v", turn.Contender.Name, errors.Join(readFailures...))
	return nil
}
