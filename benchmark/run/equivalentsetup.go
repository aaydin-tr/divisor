package run

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

const (
	equivalentSetupSamplerImage     = "divisor-benchmark/equivalent-setup-sampler"
	equivalentSetupSamplerContainer = "divisor-benchmark-equivalent-setup-sampler"
)

func (r *runner) buildEquivalentSetupSampler(ctx context.Context) error {
	r.logf("building the equivalent setup sampler image")
	_, err := r.docker.output(ctx, "build", "--quiet", "--tag", equivalentSetupSamplerImage,
		"--file", filepath.Join(r.options.BenchmarkDirectory, "equivalentsetup", "Dockerfile"), r.options.BenchmarkDirectory)
	return err
}

// sampleEquivalentSetup runs the sampler on the load generator's cores, on
// the host network, since the host itself may not reach that network.
func (r *runner) sampleEquivalentSetup(ctx context.Context, turn *plan.Turn, check *plan.EquivalentSetupCheck) error {
	r.logTurnf(turn, "%s: checking the Equivalent setup with %d requests", turn.Contender.Name, check.Requests)
	arguments := append(dockerRunOnHostNetwork(removedOnExit, equivalentSetupSamplerContainer, r.plan.Resources.LoadGeneratorCPUs),
		equivalentSetupSamplerImage, "--url", r.plan.Network.ContenderURL,
		"--requests", strconv.Itoa(check.Requests), "--connections", strconv.Itoa(check.Connections))
	output, err := r.docker.output(ctx, arguments...)
	if err != nil {
		return err
	}
	sample := recording.EquivalentSetupSample{
		Round:       turn.Round,
		ContenderID: turn.Contender.ID,
		SampleFile:  recording.EquivalentSetupSampleFile(turn.Round, turn.Contender.ID),
		Sample:      output,
	}
	r.equivalentSetupSamples = append(r.equivalentSetupSamples, sample)
	return recording.WriteResultsFile(r.options.ResultsDirectory, sample.SampleFile, output)
}
