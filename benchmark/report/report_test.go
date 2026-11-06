package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaydin-tr/divisor/benchmark/recording"
)

var update = flag.Bool("update", false, "rewrite each recorded run's expected outputs from the current Report")

// Each testdata directory is a results directory as Run leaves it (recording.json
// and the raw oha outputs) plus the section and benchmark.json Report must produce.
func TestReportOfARecordedRun(t *testing.T) {
	recordedRuns := []string{
		"normal-run",
		"failed-row",
		"never-ready",
		"manual-run-on-a-tag",
		"smoke-run",
	}
	for _, recordedRun := range recordedRuns {
		t.Run(recordedRun, func(t *testing.T) {
			resultsDirectory := filepath.Join("testdata", recordedRun)
			recorded, err := recording.Load(resultsDirectory)
			if err != nil {
				t.Fatal(err)
			}

			built, err := Build(&recorded)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			benchmarkJSON, err := built.BenchmarkJSON()
			if err != nil {
				t.Fatalf("BenchmarkJSON: %v", err)
			}

			assertMatchesExpected(t, filepath.Join(resultsDirectory, "expected-section.md"), []byte(built.Markdown()), bytes.Equal)
			assertMatchesExpected(t, filepath.Join(resultsDirectory, "expected-benchmark.json"), benchmarkJSON, sameJSON)
		})
	}
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
		t.Errorf("%s differs\n--- got ---\n%s\n--- expected ---\n%s", expectedFile, got, expected)
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
