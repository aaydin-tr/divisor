// Package recording is what a run observed, as Run writes it to the results directory and Report reads it.
package recording

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

const (
	FileName = "recording.json"

	resultsFilePermissions      = 0o600
	resultsDirectoryPermissions = 0o750
)

type Recording struct {
	Plan         plan.Plan     `json:"plan"`
	Environment  Environment   `json:"environment"`
	Measurements []Measurement `json:"measurements"`
	// EquivalentSetupSamples are there when the Plan checks the Equivalent
	// setup, one per Contender that became ready.
	EquivalentSetupSamples []EquivalentSetupSample `json:"equivalent_setup_samples,omitempty"`
}

// EquivalentSetupSample is the equivalent setup sampler's JSON output.
type EquivalentSetupSample struct {
	Round       int    `json:"round"`
	ContenderID string `json:"contender"`
	// SampleFile is relative to the results directory, slash-separated.
	SampleFile string `json:"sample_file"`
	Sample     []byte `json:"-"`
}

func EquivalentSetupSampleFile(round int, contenderID string) string {
	return path.Join(RoundDirectory(round, contenderID), "equivalent-setup-sample.json")
}

type Environment struct {
	StartedAt     time.Time `json:"started_at"`
	RunnerImage   string    `json:"runner_image"`
	Architecture  string    `json:"architecture"`
	CPUModel      string    `json:"cpu_model"`
	LoadGenerator string    `json:"load_generator"`
	// WorkflowRunURL is the GitHub Actions run that measured; empty for a local run.
	WorkflowRunURL string `json:"workflow_run_url,omitempty"`
	// ContenderImageIDs maps each Contender ID to the image ID of its Image.
	ContenderImageIDs map[string]string `json:"contender_image_ids"`
}

type Measurement struct {
	Round       int    `json:"round"`
	ContenderID string `json:"contender"`
	WorkloadID  string `json:"workload"`
	// OhaOutputFile is relative to the results directory, slash-separated.
	OhaOutputFile string `json:"oha_output_file,omitempty"`
	// Failure says why the Contender could not be measured at all.
	Failure   string `json:"failure,omitempty"`
	OhaOutput []byte `json:"-"`
}

func OhaOutputFile(round int, contenderID, workloadID string) string {
	return path.Join(RoundDirectory(round, contenderID), workloadID+".json")
}

func RoundDirectory(round int, contenderID string) string {
	return path.Join("raw", fmt.Sprintf("round-%d", round), contenderID)
}

func Save(resultsDirectory string, recorded *Recording) error {
	encoded, err := json.MarshalIndent(recorded, "", "  ")
	if err != nil {
		return err
	}
	return WriteResultsFile(resultsDirectory, FileName, append(encoded, '\n'))
}

// WriteResultsFile writes a file at a slash-separated path under the results
// directory, creating the directories on the way.
func WriteResultsFile(resultsDirectory, slashSeparatedPath string, content []byte) error {
	resultsFile := filepath.Join(resultsDirectory, filepath.FromSlash(slashSeparatedPath))
	if err := os.MkdirAll(filepath.Dir(resultsFile), resultsDirectoryPermissions); err != nil {
		return err
	}
	return os.WriteFile(resultsFile, content, resultsFilePermissions)
}

func Load(resultsDirectory string) (Recording, error) {
	encoded, err := os.ReadFile(filepath.Join(resultsDirectory, FileName))
	if err != nil {
		return Recording{}, err
	}
	var recorded Recording
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		return Recording{}, fmt.Errorf("reading %s: %w", FileName, err)
	}
	for i := range recorded.Measurements {
		measurement := &recorded.Measurements[i]
		if measurement.OhaOutputFile == "" {
			continue
		}
		measurement.OhaOutput, err = os.ReadFile(filepath.Join(resultsDirectory, filepath.FromSlash(measurement.OhaOutputFile)))
		if err != nil {
			return Recording{}, err
		}
	}
	for i := range recorded.EquivalentSetupSamples {
		sample := &recorded.EquivalentSetupSamples[i]
		sample.Sample, err = os.ReadFile(filepath.Join(resultsDirectory, filepath.FromSlash(sample.SampleFile)))
		if err != nil {
			return Recording{}, err
		}
	}
	return recorded, nil
}
