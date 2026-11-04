// Package run executes a Plan with Docker and oha and records what happened; it decides nothing.
package run

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/oha"
	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

type Options struct {
	// CheckoutDirectory is the divisor checkout built when the Plan says so.
	CheckoutDirectory string
	// BenchmarkDirectory is the benchmark module holding the Benchmark backend,
	// load generator and Contender definitions; it can be newer than the checkout.
	BenchmarkDirectory string
	ResultsDirectory   string
	Log                io.Writer
}

type runner struct {
	plan    *plan.Plan
	options Options
	docker  docker

	// contendersWithoutImage maps a Contender ID to why its image could not be pulled or
	// built; each of its turns is a failed row instead of a run error.
	contendersWithoutImage map[string]string
	equivalentSetupSamples []recording.EquivalentSetupSample
}

func Execute(ctx context.Context, executed *plan.Plan, options Options) (*recording.Recording, error) {
	r := &runner{plan: executed, options: options, contendersWithoutImage: map[string]string{}}
	startedAt := time.Now()

	r.docker.removeContainers(r.containerNames()...)
	defer r.docker.removeContainers(r.containerNames()...)

	if err := r.buildImages(ctx); err != nil {
		return nil, err
	}
	environment, err := r.recordEnvironment(ctx, startedAt)
	if err != nil {
		return nil, err
	}
	if err := r.startBenchmarkBackends(ctx); err != nil {
		return nil, err
	}

	var measurements []recording.Measurement
	turns := executed.Turns()
	for i := range turns {
		turnMeasurements, err := r.runTurn(ctx, &turns[i])
		if err != nil {
			return nil, err
		}
		measurements = append(measurements, turnMeasurements...)
	}
	return &recording.Recording{
		Plan:                   *executed,
		Environment:            environment,
		Measurements:           measurements,
		EquivalentSetupSamples: r.equivalentSetupSamples,
	}, nil
}

func (r *runner) startBenchmarkBackends(ctx context.Context) error {
	for _, benchmarkBackend := range r.plan.Network.BenchmarkBackends {
		r.logf("starting Benchmark backend %s on %s", benchmarkBackend.Name, benchmarkBackend.Address)
		arguments := append(dockerRunOnHostNetwork(detached, benchmarkBackendContainer(benchmarkBackend), r.plan.Resources.BackendCPUs),
			benchmarkBackendImage, "--listen", benchmarkBackend.Address, "--name", benchmarkBackend.Name)
		if _, err := r.docker.output(ctx, arguments...); err != nil {
			return err
		}
	}
	for _, benchmarkBackend := range r.plan.Network.BenchmarkBackends {
		notReadyReason, err := r.waitUntilReady(ctx, "http://"+benchmarkBackend.Address+"/")
		if err != nil {
			return err
		}
		if notReadyReason != "" {
			return fmt.Errorf("Benchmark backend %s not ready within %v: %s", benchmarkBackend.Name, r.plan.Schedule.ReadinessTimeout(), notReadyReason)
		}
	}
	return nil
}

func (r *runner) runTurn(ctx context.Context, turn *plan.Turn) ([]recording.Measurement, error) {
	if failure, withoutImage := r.contendersWithoutImage[turn.Contender.ID]; withoutImage {
		return failedMeasurements(turn, failure), nil
	}
	r.logTurnf(turn, "starting %s", turn.Contender.Name)
	memoryLimit := r.plan.Resources.ContenderMemoryLimit()
	arguments := append(dockerRunOnHostNetwork(detached, contenderContainer, r.plan.Resources.ContenderCPUs),
		"--memory", memoryLimit, "--memory-swap", memoryLimit, contenderImage(&turn.Contender))
	if _, err := r.docker.output(ctx, arguments...); err != nil {
		return nil, err
	}
	defer r.stopContender(turn)

	notReadyReason, err := r.waitUntilReady(ctx, r.plan.Network.ContenderURL)
	if err != nil {
		return nil, err
	}
	if notReadyReason != "" {
		failure := fmt.Sprintf("not ready within %v: %s", r.plan.Schedule.ReadinessTimeout(), notReadyReason)
		r.logTurnf(turn, "%s %s", turn.Contender.Name, failure)
		return failedMeasurements(turn, failure), nil
	}
	if check := r.plan.Schedule.EquivalentSetupCheck; check != nil {
		if err := r.sampleEquivalentSetup(ctx, turn, check); err != nil {
			return nil, err
		}
	}

	measurements := make([]recording.Measurement, 0, len(turn.Workloads))
	for _, workload := range turn.Workloads {
		measurement, err := r.measureWorkload(ctx, turn, workload)
		if err != nil {
			return nil, err
		}
		measurements = append(measurements, measurement)
	}
	return measurements, nil
}

func (r *runner) measureWorkload(ctx context.Context, turn *plan.Turn, workload plan.Workload) (recording.Measurement, error) {
	workerThreads := r.plan.Resources.LoadGeneratorCores()
	if warmUp := r.plan.Schedule.WarmUp(); warmUp > 0 {
		r.logTurnf(turn, "%s: %s warm-up, %v", turn.Contender.Name, workload.Name, warmUp)
		if _, err := r.runLoadGenerator(ctx, oha.WorkloadArguments(workload, r.plan.Network.ContenderURL, warmUp, workerThreads)); err != nil {
			return recording.Measurement{}, err
		}
	}

	r.logTurnf(turn, "%s: %s, measuring %v", turn.Contender.Name, workload.Name, r.plan.Schedule.Measurement())
	output, err := r.runLoadGenerator(ctx, oha.WorkloadArguments(workload, r.plan.Network.ContenderURL, r.plan.Schedule.Measurement(), workerThreads))
	if err != nil {
		return recording.Measurement{}, err
	}
	measurement := recording.Measurement{
		Round:         turn.Round,
		ContenderID:   turn.Contender.ID,
		WorkloadID:    workload.ID,
		OhaOutputFile: recording.OhaOutputFile(turn.Round, turn.Contender.ID, workload.ID),
		OhaOutput:     output,
	}
	return measurement, recording.WriteResultsFile(r.options.ResultsDirectory, measurement.OhaOutputFile, output)
}

func (r *runner) runLoadGenerator(ctx context.Context, ohaArguments []string) ([]byte, error) {
	arguments := append(dockerRunOnHostNetwork(removedOnExit, loadGeneratorContainer, r.plan.Resources.LoadGeneratorCPUs),
		loadGeneratorImage)
	return r.docker.output(ctx, append(arguments, ohaArguments...)...)
}

// stopContender keeps the Contender's log next to its raw results, then
// removes it, whatever state the turn ended in.
func (r *runner) stopContender(turn *plan.Turn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if logs, err := r.docker.combinedOutput(ctx, "logs", contenderContainer); err == nil {
		_ = recording.WriteResultsFile(r.options.ResultsDirectory, recording.ContenderLogFile(turn.Round, turn.Contender.ID), logs)
	}
	r.docker.removeContainers(contenderContainer)
}

func failedMeasurements(turn *plan.Turn, failure string) []recording.Measurement {
	measurements := make([]recording.Measurement, 0, len(turn.Workloads))
	for _, workload := range turn.Workloads {
		measurements = append(measurements, recording.Measurement{
			Round:       turn.Round,
			ContenderID: turn.Contender.ID,
			WorkloadID:  workload.ID,
			Failure:     failure,
		})
	}
	return measurements
}

func (r *runner) containerNames() []string {
	names := []string{contenderContainer, loadGeneratorContainer, equivalentSetupSamplerContainer}
	for _, benchmarkBackend := range r.plan.Network.BenchmarkBackends {
		names = append(names, benchmarkBackendContainer(benchmarkBackend))
	}
	return names
}

const (
	detached      = "--detach"
	removedOnExit = "--rm"
)

func dockerRunOnHostNetwork(lifetime, containerName, cpus string) []string {
	return []string{"run", lifetime, "--name", containerName, "--network", "host", "--cpuset-cpus", cpus}
}

func (r *runner) logTurnf(turn *plan.Turn, format string, arguments ...any) {
	r.logf("round %d/%d: "+format, append([]any{turn.Round, r.plan.Schedule.Rounds}, arguments...)...)
}

func (r *runner) logf(format string, arguments ...any) {
	fmt.Fprintf(r.options.Log, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05")}, arguments...)...)
}
