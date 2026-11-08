package recording

import (
	"reflect"
	"testing"
)

// benchmark/README.md tells readers where to find these files.
func TestRawResultFilesAreWhereTheREADMESays(t *testing.T) {
	for got, want := range map[string]string{
		OhaOutputFile(1, "nginx", "throughput"): "raw/round-1/nginx/throughput.json",
		ContenderLogFile(2, "divisor"):          "raw/round-2/divisor/contender.log",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestLoadReadsBackWhatRunSaved(t *testing.T) {
	resultsDirectory := t.TempDir()
	peakMemoryBytes := int64(12 << 20)
	measured := Measurement{
		Round:           1,
		ContenderID:     "divisor",
		WorkloadID:      "throughput",
		OhaOutputFile:   OhaOutputFile(1, "divisor", "throughput"),
		PeakMemoryBytes: &peakMemoryBytes,
		OhaOutput:       []byte(`{"summary":{"requestsPerSec":20000}}`),
	}
	neverReady := Measurement{Round: 1, ContenderID: "caddy", WorkloadID: "throughput", Failure: "not ready within 30s"}
	saved := Recording{Measurements: []Measurement{measured, neverReady}}
	if err := WriteResultsFile(resultsDirectory, measured.OhaOutputFile, measured.OhaOutput); err != nil {
		t.Fatal(err)
	}
	if err := Save(resultsDirectory, &saved); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(resultsDirectory)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(loaded.Measurements, saved.Measurements) {
		t.Errorf("loaded measurements %+v, want %+v", loaded.Measurements, saved.Measurements)
	}
}
