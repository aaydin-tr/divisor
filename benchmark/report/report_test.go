package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

var update = flag.Bool("update", false, "rewrite testdata's expected section and benchmark.json from the current Report")

const (
	measuredCommit   = "aa36fa6c0ffee0000000000000000000000000ee"
	benchmarkCommit  = "b3c4f1e0ffee0000000000000000000000000bb1"
	releaseTagCommit = "5e96045d0ffee000000000000000000000000001"
	workflowRunURL   = "https://github.com/aaydin-tr/divisor/actions/runs/123456789"
)

// upstreamVersions stand in for the contenders/ FROM pins, so a Dependabot bump leaves the expected files alone.
var upstreamVersions = map[string]string{"caddy": "2.11.7", "nginx": "1.31.8", "traefik": "v3.7.17"}

var localMachine = recording.Environment{
	StartedAt:         time.Date(2026, time.November, 8, 19, 30, 0, 0, time.UTC),
	RunnerImage:       "local machine running Docker Desktop (containerized)",
	Architecture:      "amd64",
	CPUModel:          "AMD Ryzen 5 5500",
	LoadGenerator:     "oha 1.16.0",
	ContenderImageIDs: map[string]string{"divisor": "sha256:divisor", "caddy": "sha256:caddy", "nginx": "sha256:nginx", "traefik": "sha256:traefik"},
}

// fullRunRounds holds rounds 1 to 3 of each Contender; each number's median is the middle of its three values.
// Throughput ranks nginx, divisor, Traefik, Caddy and Fixed-rate latency nginx, divisor, Caddy, Traefik.
var fullRunRounds = map[string]map[string][]Numbers{
	plan.Throughput.ID: {
		"divisor": {
			{RequestsPerSecond: 21000, P50Milliseconds: 3.00, P99Milliseconds: 5.20, P999Milliseconds: 10.00, PeakMemoryMB: megabytes(12.5)},
			{RequestsPerSecond: 20000, P50Milliseconds: 3.20, P99Milliseconds: 5.00, P999Milliseconds: 11.00, PeakMemoryMB: megabytes(12.0)},
			{RequestsPerSecond: 19000, P50Milliseconds: 2.80, P99Milliseconds: 4.80, P999Milliseconds: 9.00, PeakMemoryMB: megabytes(11.5)},
		},
		"caddy": {
			{RequestsPerSecond: 7000, P50Milliseconds: 9.00, P99Milliseconds: 20.00, P999Milliseconds: 40.00, PeakMemoryMB: megabytes(24.0)},
			{RequestsPerSecond: 7200, P50Milliseconds: 8.80, P99Milliseconds: 19.00, P999Milliseconds: 38.00, PeakMemoryMB: megabytes(24.5)},
			{RequestsPerSecond: 6800, P50Milliseconds: 9.20, P99Milliseconds: 21.00, P999Milliseconds: 42.00, PeakMemoryMB: megabytes(23.5)},
		},
		"nginx": {
			{RequestsPerSecond: 24000, P50Milliseconds: 2.60, P99Milliseconds: 4.70, P999Milliseconds: 9.50, PeakMemoryMB: megabytes(6.5)},
			{RequestsPerSecond: 26000, P50Milliseconds: 2.40, P99Milliseconds: 4.30, P999Milliseconds: 8.50, PeakMemoryMB: megabytes(5.5)},
			{RequestsPerSecond: 25000, P50Milliseconds: 2.50, P99Milliseconds: 4.50, P999Milliseconds: 9.00, PeakMemoryMB: megabytes(6.0)},
		},
		"traefik": {
			{RequestsPerSecond: 8800, P50Milliseconds: 7.20, P99Milliseconds: 16.50, P999Milliseconds: 31.00, PeakMemoryMB: megabytes(41.0)},
			{RequestsPerSecond: 9000, P50Milliseconds: 7.00, P99Milliseconds: 16.00, P999Milliseconds: 30.00, PeakMemoryMB: megabytes(40.0)},
			{RequestsPerSecond: 9200, P50Milliseconds: 6.80, P99Milliseconds: 15.50, P999Milliseconds: 29.00, PeakMemoryMB: megabytes(39.0)},
		},
	},
	plan.FixedRateLatency.ID: {
		"divisor": {
			{RequestsPerSecond: 4999.6, P50Milliseconds: 0.35, P99Milliseconds: 0.90, P999Milliseconds: 5.00},
			{RequestsPerSecond: 4999.8, P50Milliseconds: 0.37, P99Milliseconds: 1.00, P999Milliseconds: 6.00},
			{RequestsPerSecond: 4999.7, P50Milliseconds: 0.33, P99Milliseconds: 0.80, P999Milliseconds: 4.00},
		},
		"caddy": {
			{RequestsPerSecond: 4999.5, P50Milliseconds: 0.80, P99Milliseconds: 20.00, P999Milliseconds: 60.00},
			{RequestsPerSecond: 4999.4, P50Milliseconds: 0.85, P99Milliseconds: 22.00, P999Milliseconds: 70.00},
			{RequestsPerSecond: 4999.6, P50Milliseconds: 0.75, P99Milliseconds: 18.00, P999Milliseconds: 50.00},
		},
		"nginx": {
			{RequestsPerSecond: 4999.9, P50Milliseconds: 0.32, P99Milliseconds: 0.65, P999Milliseconds: 4.20},
			{RequestsPerSecond: 4999.8, P50Milliseconds: 0.30, P99Milliseconds: 0.60, P999Milliseconds: 4.00},
			{RequestsPerSecond: 4999.7, P50Milliseconds: 0.28, P99Milliseconds: 0.55, P999Milliseconds: 3.80},
		},
		"traefik": {
			{RequestsPerSecond: 4999.6, P50Milliseconds: 0.90, P99Milliseconds: 15.00, P999Milliseconds: 45.00},
			{RequestsPerSecond: 4999.5, P50Milliseconds: 0.95, P99Milliseconds: 16.00, P999Milliseconds: 50.00},
			{RequestsPerSecond: 4999.7, P50Milliseconds: 0.85, P99Milliseconds: 14.00, P999Milliseconds: 40.00},
		},
	},
}

// steadyNumbers is what every round of an unusual run measures, per Workload.
var steadyNumbers = map[string]Numbers{
	plan.Throughput.ID:       {RequestsPerSecond: 20000, P50Milliseconds: 3, P99Milliseconds: 5, P999Milliseconds: 10, PeakMemoryMB: megabytes(12)},
	plan.FixedRateLatency.ID: {RequestsPerSecond: 5000, P50Milliseconds: 0.3, P99Milliseconds: 0.6, P999Milliseconds: 4},
}

var everyRequestAnswered200 = map[int]int{200: 1000}

func TestReportOfAFullRun(t *testing.T) {
	recorded := recordingOf(t, decidedPlan(t, localRunOnMain()), func(round int, contenderID, workloadID string) Numbers {
		return fullRunRounds[workloadID][contenderID][round-1]
	})

	built, err := Build(&recorded)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	benchmarkJSON, err := built.BenchmarkJSON()
	if err != nil {
		t.Fatalf("BenchmarkJSON: %v", err)
	}

	assertMatchesExpected(t, filepath.Join("testdata", "expected-section.md"), []byte(built.Markdown()), bytes.Equal)
	assertMatchesExpected(t, filepath.Join("testdata", "expected-benchmark.json"), benchmarkJSON, sameJSON)
}

func TestReportOfAnUnusualRun(t *testing.T) {
	const notReady = "not ready within 30s: 1 request failed: Connection refused (os error 111)"
	tests := []struct {
		name            string
		inputs          plan.Inputs
		changeRecording func(t *testing.T, recorded *recording.Recording)
		wantInSection   []string
		notInSection    []string
		// wantInBenchmarkJSON maps a dotted path, where a number indexes an array, to its value or to absent.
		wantInBenchmarkJSON map[string]any
	}{
		{
			name:   "a round answered 503",
			inputs: localRunOnMain(),
			changeRecording: func(t *testing.T, recorded *recording.Recording) {
				divisorMeasurement(t, recorded, 2, plan.Throughput.ID).OhaOutput = ohaOutput(t, steadyNumbers[plan.Throughput.ID], map[int]int{503: 51200})
			},
			wantInSection: []string{
				"| divisor | main@aa36fa6 | failed | failed | failed | failed |\n\nFailed:\n\n" +
					"- divisor, round 2: answered 503 to 51200 requests\n\n### Fixed-rate latency",
				"| divisor | main@aa36fa6 | 0.30 ms | 0.60 ms | 4.00 ms |\n",
			},
			wantInBenchmarkJSON: map[string]any{
				"workloads.0.results.0.status":                       "failed",
				"workloads.0.results.0.median":                       nil,
				"workloads.0.results.0.spread":                       nil,
				"workloads.0.results.0.rounds.0.requests_per_second": 20000.0,
				"workloads.0.results.0.rounds.0.peak_memory_mb":      12.0,
				"workloads.0.results.0.rounds.1.failure":             "answered 503 to 51200 requests",
				"workloads.0.results.0.rounds.1.requests_per_second": absent,
				"workloads.1.results.0.status":                       "ok",
			},
		},
		{
			name:   "divisor never became ready in round 1",
			inputs: localRunOnMain(),
			changeRecording: func(t *testing.T, recorded *recording.Recording) {
				for _, workload := range recorded.Plan.Workloads {
					*divisorMeasurement(t, recorded, 1, workload.ID) = recording.Measurement{Round: 1, ContenderID: "divisor", WorkloadID: workload.ID, Failure: notReady}
				}
			},
			wantInSection: []string{
				"| divisor | main@aa36fa6 | failed | failed | failed | failed |\n\nFailed:\n\n- divisor, round 1: " + notReady + "\n\n### Fixed-rate latency",
				"| divisor | main@aa36fa6 | failed | failed | failed |\n\nFailed:\n\n- divisor, round 1: " + notReady + "\n",
			},
			wantInBenchmarkJSON: map[string]any{
				"workloads.0.results.0.status":                       "failed",
				"workloads.0.results.0.rounds.0.failure":             notReady,
				"workloads.0.results.0.rounds.0.requests_per_second": absent,
				"workloads.1.results.0.status":                       "failed",
				"workloads.1.results.0.rounds.0.failure":             notReady,
			},
		},
		{
			name:                "a smoke run",
			inputs:              plan.Inputs{Trigger: plan.SmokeTrigger, Ref: "main", Commit: measuredCommit, BenchmarkCommit: measuredCommit},
			wantInSection:       []string{"These numbers come from one short smoke round (3s), not a measurement."},
			notInSection:        []string{"median"},
			wantInBenchmarkJSON: map[string]any{"run.mode": "smoke", "method.rounds": 1.0},
		},
		{
			name:   "a manual run on a release tag",
			inputs: plan.Inputs{Trigger: plan.ManualTrigger, Ref: "v1.0.0", Tags: []string{"v1.0.0"}, Commit: releaseTagCommit, BenchmarkCommit: benchmarkCommit},
			changeRecording: func(_ *testing.T, recorded *recording.Recording) {
				recorded.Environment.WorkflowRunURL = workflowRunURL
			},
			wantInSection: []string{
				"[How this is measured](https://github.com/aaydin-tr/divisor/blob/" + benchmarkCommit + "/benchmark/README.md) · [Workflow run](" + workflowRunURL + ")\n",
				"| divisor | v1.0.0 | 20,000 |",
			},
			notInSection: []string{releaseTagCommit},
			wantInBenchmarkJSON: map[string]any{
				"run.ref":            "v1.0.0",
				"run.commit":         releaseTagCommit,
				"contenders.0.image": "ghcr.io/aaydin-tr/divisor:v1.0.0",
			},
		},
		{
			name:          "a recording without the benchmark commit",
			inputs:        plan.Inputs{Trigger: plan.LocalTrigger, Ref: "main", Commit: measuredCommit},
			wantInSection: []string{"[How this is measured](https://github.com/aaydin-tr/divisor/blob/main/benchmark/README.md)\n"},
		},
		{
			name:   "no round could read the peak memory",
			inputs: localRunOnMain(),
			changeRecording: func(_ *testing.T, recorded *recording.Recording) {
				for i := range recorded.Measurements {
					recorded.Measurements[i].PeakMemoryBytes = nil
				}
			},
			wantInSection: []string{
				"| divisor | main@aa36fa6 | 20,000 | 3.00 ms | 5.00 ms | n/a |\n\n" +
					"Peak memory n/a: the run could not read the proxy container's `memory.peak` file, which needs cgroup v2 and Linux 5.19 or later.\n\n" +
					"### Fixed-rate latency",
			},
			wantInBenchmarkJSON: map[string]any{
				"workloads.0.results.0.rounds.0.peak_memory_mb":    absent,
				"workloads.0.results.0.median.requests_per_second": 20000.0,
				"workloads.0.results.0.median.peak_memory_mb":      absent,
				"workloads.0.results.0.spread.peak_memory":         absent,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorded := divisorOnlyRun(t, test.inputs)
			if test.changeRecording != nil {
				test.changeRecording(t, &recorded)
			}

			built, err := Build(&recorded)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			section := built.Markdown()
			for _, want := range test.wantInSection {
				if !strings.Contains(section, want) {
					t.Errorf("section lacks\n%s\n--- section ---\n%s", want, section)
				}
			}
			for _, unwanted := range test.notInSection {
				if strings.Contains(section, unwanted) {
					t.Errorf("section has %q\n--- section ---\n%s", unwanted, section)
				}
			}
			assertBenchmarkJSONFields(t, built, test.wantInBenchmarkJSON)
		})
	}
}

func localRunOnMain() plan.Inputs {
	return plan.Inputs{Trigger: plan.LocalTrigger, Ref: "main", Commit: measuredCommit, BenchmarkCommit: measuredCommit}
}

func decidedPlan(t *testing.T, inputs plan.Inputs) plan.Plan {
	t.Helper()
	decided, err := plan.Decide(&inputs)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	for i := range decided.Contenders {
		contender := &decided.Contenders[i]
		if version, upstream := upstreamVersions[contender.ID]; upstream {
			contender.Version, contender.Image = version, contender.ID+":"+version
		}
	}
	return decided
}

func divisorOnlyRun(t *testing.T, inputs plan.Inputs) recording.Recording {
	t.Helper()
	decided := decidedPlan(t, inputs)
	decided.Contenders = decided.Contenders[:1]
	return recordingOf(t, decided, func(_ int, _, workloadID string) Numbers { return steadyNumbers[workloadID] })
}

// recordingOf is what Run records when every round measures numbersOf, Throughput reading the peak memory.
func recordingOf(t *testing.T, decided plan.Plan, numbersOf func(round int, contenderID, workloadID string) Numbers) recording.Recording {
	t.Helper()
	recorded := recording.Recording{Plan: decided, Environment: localMachine}
	for _, turn := range decided.Turns() {
		for _, workload := range turn.Workloads {
			numbers := numbersOf(turn.Round, turn.Contender.ID, workload.ID)
			measurement := recording.Measurement{
				Round:         turn.Round,
				ContenderID:   turn.Contender.ID,
				WorkloadID:    workload.ID,
				OhaOutputFile: recording.OhaOutputFile(turn.Round, turn.Contender.ID, workload.ID),
				OhaOutput:     ohaOutput(t, numbers, everyRequestAnswered200),
			}
			if workload.ReadsPeakMemory {
				measurement.PeakMemoryBytes = peakMemoryBytesOf(numbers.PeakMemoryMB)
			}
			recorded.Measurements = append(recorded.Measurements, measurement)
		}
	}
	return recorded
}

const millisecondsPerSecond = 1000

// ohaOutput writes only the fields oha.ParseResult reads.
func ohaOutput(t *testing.T, numbers Numbers, statusCodeDistribution map[int]int) []byte {
	t.Helper()
	output, err := json.Marshal(map[string]any{
		"summary": map[string]float64{"requestsPerSec": numbers.RequestsPerSecond},
		"latencyPercentiles": map[string]float64{
			"p50":   numbers.P50Milliseconds / millisecondsPerSecond,
			"p99":   numbers.P99Milliseconds / millisecondsPerSecond,
			"p99.9": numbers.P999Milliseconds / millisecondsPerSecond,
		},
		"statusCodeDistribution": statusCodeDistribution,
	})
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func megabytes(value float64) *float64 {
	return &value
}

func peakMemoryBytesOf(peakMemoryMB *float64) *int64 {
	if peakMemoryMB == nil {
		return nil
	}
	peakMemoryBytes := int64(*peakMemoryMB * bytesPerMiB)
	return &peakMemoryBytes
}

func divisorMeasurement(t *testing.T, recorded *recording.Recording, round int, workloadID string) *recording.Measurement {
	t.Helper()
	measurement, found := findMeasurement(recorded.Measurements, round, "divisor", workloadID)
	if !found {
		t.Fatalf("no divisor measurement of %s in round %d", workloadID, round)
	}
	return measurement
}

type absentField struct{}

var absent = absentField{}

func assertBenchmarkJSONFields(t *testing.T, built *Report, wantFields map[string]any) {
	t.Helper()
	encoded, err := built.BenchmarkJSON()
	if err != nil {
		t.Fatalf("BenchmarkJSON: %v", err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for path, want := range wantFields {
		got, present := fieldAt(document, path)
		switch {
		case want == absent && present:
			t.Errorf("benchmark.json %s = %v, want it absent", path, got)
		case want != absent && !present:
			t.Errorf("benchmark.json has no %s, want %v", path, want)
		case want != absent && !reflect.DeepEqual(got, want):
			t.Errorf("benchmark.json %s = %v, want %v", path, got, want)
		}
	}
}

func fieldAt(document any, path string) (field any, present bool) {
	field = document
	for _, step := range strings.Split(path, ".") {
		switch node := field.(type) {
		case map[string]any:
			if field, present = node[step]; !present {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(step)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			field = node[index]
		default:
			return nil, false
		}
	}
	return field, true
}

func assertMatchesExpected(t *testing.T, expectedFile string, got []byte, matches func(got, expected []byte) bool) {
	t.Helper()
	if *update {
		if err := os.WriteFile(expectedFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatal(err)
	}
	if !matches(got, expected) {
		t.Errorf("%s differs (-update rewrites it)\n--- got ---\n%s\n--- expected ---\n%s", expectedFile, got, expected)
	}
}

// sameJSON compares content, not layout, so an expected file can be written by hand.
func sameJSON(got, expected []byte) bool {
	var gotValue, expectedValue any
	if json.Unmarshal(got, &gotValue) != nil || json.Unmarshal(expected, &expectedValue) != nil {
		return false
	}
	return reflect.DeepEqual(gotValue, expectedValue)
}

func TestPeakMemoryHasAMedianOnlyWhenEveryRoundReadIt(t *testing.T) {
	roundThatRead := func(peakMemoryMB float64) RoundResult {
		return RoundResult{Numbers: Numbers{PeakMemoryMB: &peakMemoryMB}}
	}

	median, spread := medianAndSpread([]RoundResult{roundThatRead(30), roundThatRead(10), roundThatRead(20)})
	if median.PeakMemoryMB == nil || *median.PeakMemoryMB != 20 || spread.PeakMemory == nil || *spread.PeakMemory != 1 {
		t.Errorf("every round read it: median %v, spread %v, want 20 and 1", median.PeakMemoryMB, spread.PeakMemory)
	}

	median, spread = medianAndSpread([]RoundResult{roundThatRead(30), {}, roundThatRead(20)})
	if median.PeakMemoryMB != nil || spread.PeakMemory != nil {
		t.Errorf("a round did not read it: median %v, spread %v, want neither (n/a)", median.PeakMemoryMB, spread.PeakMemory)
	}
}
