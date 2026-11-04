package recording

import "testing"

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
