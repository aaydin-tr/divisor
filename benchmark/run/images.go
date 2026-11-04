package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

func (r *runner) buildImages(ctx context.Context) error {
	for _, contender := range r.plan.Contenders {
		if contender.ImageFromCheckout {
			if err := r.buildDivisorFromCheckout(ctx, contender.Version); err != nil {
				return err
			}
			break
		}
	}

	if err := r.pullPublishedImages(ctx); err != nil {
		return err
	}

	r.logf("building the Benchmark backend and load generator images")
	if _, err := r.docker.output(ctx, "build", "--quiet", "--tag", benchmarkBackendImage,
		"--file", filepath.Join(r.options.BenchmarkDirectory, "backend", "Dockerfile"), r.options.BenchmarkDirectory); err != nil {
		return err
	}
	if _, err := r.docker.output(ctx, "build", "--quiet", "--tag", loadGeneratorImage,
		filepath.Join(r.options.BenchmarkDirectory, "loadgenerator")); err != nil {
		return err
	}
	if r.plan.Schedule.EquivalentSetupCheck != nil {
		if err := r.buildEquivalentSetupSampler(ctx); err != nil {
			return err
		}
	}

	for i := range r.plan.Contenders {
		contender := &r.plan.Contenders[i]
		if _, unpulled := r.contendersWithoutImage[contender.ID]; unpulled {
			continue
		}
		r.logf("building Contender %s on %s", contender.Name, contender.Image)
		arguments := []string{"build", "--quiet", "--tag", contenderImage(contender)}
		for _, name := range sortedBuildArgNames(contender.BuildArgs) {
			arguments = append(arguments, "--build-arg", name+"="+contender.BuildArgs[name])
		}
		arguments = append(arguments, filepath.Join(r.options.BenchmarkDirectory, "contenders", contender.Definition))
		if _, err := r.docker.output(ctx, arguments...); err != nil {
			if ctx.Err() != nil {
				return err
			}
			r.contendersWithoutImage[contender.ID] = unbuiltImageFailure(contender.Image, err)
			r.logf("Contender %s: %s", contender.Name, r.contendersWithoutImage[contender.ID])
		}
	}
	return nil
}

// pullPublishedImages pulls every Contender image not built here: building
// FROM it does not put it in the image store, where its image ID is read.
func (r *runner) pullPublishedImages(ctx context.Context) error {
	for _, contender := range r.plan.Contenders {
		if contender.ImageFromCheckout {
			continue
		}
		r.logf("pulling %s", contender.Image)
		if _, err := r.docker.output(ctx, "pull", "--quiet", contender.Image); err != nil {
			if ctx.Err() != nil {
				return err
			}
			r.contendersWithoutImage[contender.ID] = unpulledImageFailure(contender.Image, err)
			r.logf("Contender %s: %s", contender.Name, r.contendersWithoutImage[contender.ID])
		}
	}
	return nil
}

// unpulledImageFailure keeps the daemon's reason out of the docker pull error.
func unpulledImageFailure(image string, dockerPullError error) string {
	lines := strings.Split(strings.TrimSpace(dockerPullError.Error()), "\n")
	cause := lines[len(lines)-1]
	if _, daemonReason, found := strings.Cut(cause, "Error response from daemon: "); found {
		cause = daemonReason
	}
	return fmt.Sprintf("image %s not pulled: %s", image, cause)
}

// unbuiltImageFailure keeps the line BuildKit starts with "ERROR:", the cause,
// out of the whole docker build output.
func unbuiltImageFailure(image string, dockerBuildError error) string {
	lines := strings.Split(strings.TrimSpace(dockerBuildError.Error()), "\n")
	cause := lines[len(lines)-1]
	for _, line := range lines {
		if after, found := strings.CutPrefix(line, "ERROR: "); found {
			cause = after
		}
	}
	return fmt.Sprintf("image %s not built: %s", image, cause)
}

// buildDivisorFromCheckout builds the release image shape: the static
// linux/amd64 binary goreleaser would build, in the release Dockerfile.
func (r *runner) buildDivisorFromCheckout(ctx context.Context, version string) error {
	r.logf("building divisor %s from the checkout into %s", version, plan.CheckoutDivisorImage)
	buildContext, err := os.MkdirTemp("", "divisor-benchmark-release-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildContext)

	goBuild := exec.CommandContext(ctx, "go", "build", //nolint:gosec
		"-ldflags", fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s", version, r.plan.ShortCommit()),
		"-o", filepath.Join(buildContext, "linux", "amd64", "divisor"), ".")
	goBuild.Dir = r.options.CheckoutDirectory
	goBuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := goBuild.CombinedOutput(); err != nil {
		return fmt.Errorf("building divisor from the checkout: %w: %s", err, output)
	}

	_, err = r.docker.output(ctx, "build", "--quiet", "--platform", "linux/amd64", "--tag", plan.CheckoutDivisorImage,
		"--file", filepath.Join(r.options.CheckoutDirectory, "Dockerfile"), buildContext)
	return err
}

func sortedBuildArgNames(buildArgs map[string]string) []string {
	names := make([]string, 0, len(buildArgs))
	for name := range buildArgs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
