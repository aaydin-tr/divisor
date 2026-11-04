package run

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/recording"
)

func (r *runner) recordEnvironment(ctx context.Context, startedAt time.Time) (recording.Environment, error) {
	dockerHost, err := r.docker.output(ctx, "info", "--format", "{{.Architecture}}\t{{.OperatingSystem}}")
	if err != nil {
		return recording.Environment{}, err
	}
	architecture, operatingSystem, _ := strings.Cut(strings.TrimSpace(string(dockerHost)), "\t")

	cpuInfo, err := r.docker.output(ctx, "run", "--rm", "--network", "none", "--entrypoint", "cat", benchmarkBackendImage, "/proc/cpuinfo")
	if err != nil {
		return recording.Environment{}, err
	}

	loadGeneratorVersion, err := r.docker.output(ctx, "run", "--rm", loadGeneratorImage, "--version")
	if err != nil {
		return recording.Environment{}, err
	}

	contenderImageIDs := map[string]string{}
	for _, contender := range r.plan.Contenders {
		if _, withoutImage := r.contendersWithoutImage[contender.ID]; withoutImage {
			continue
		}
		imageID, err := r.docker.output(ctx, "image", "inspect", "--format", "{{.Id}}", contender.Image)
		if err != nil {
			return recording.Environment{}, err
		}
		contenderImageIDs[contender.ID] = strings.TrimSpace(string(imageID))
	}

	return recording.Environment{
		StartedAt:         startedAt.UTC(),
		RunnerImage:       runnerImage(operatingSystem),
		Architecture:      goArchitectureName(architecture),
		CPUModel:          cpuModel(string(cpuInfo)),
		LoadGenerator:     strings.TrimSpace(string(loadGeneratorVersion)),
		WorkflowRunURL:    workflowRunURL(),
		ContenderImageIDs: contenderImageIDs,
	}, nil
}

// runnerImage reads the variables GitHub-hosted runners set.
func runnerImage(dockerOperatingSystem string) string {
	if imageOS, imageVersion := os.Getenv("ImageOS"), os.Getenv("ImageVersion"); imageOS != "" {
		return strings.TrimSpace("GitHub-hosted " + imageOS + " " + imageVersion)
	}
	return "local machine running " + dockerOperatingSystem
}

// workflowRunURL reads the variables GitHub Actions sets.
func workflowRunURL() string {
	serverURL, repository, runID := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID")
	if serverURL == "" || repository == "" || runID == "" {
		return ""
	}
	return serverURL + "/" + repository + "/actions/runs/" + runID
}

func goArchitectureName(dockerArchitecture string) string {
	switch dockerArchitecture {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	}
	return dockerArchitecture
}

func cpuModel(cpuInfo string) string {
	for _, line := range strings.Split(cpuInfo, "\n") {
		if name, value, found := strings.Cut(line, ":"); found && strings.TrimSpace(name) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown CPU"
}
