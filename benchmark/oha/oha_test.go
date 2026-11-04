package oha

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

func TestThroughputKeepsEveryConnectionBusy(t *testing.T) {
	got := strings.Join(WorkloadArguments(plan.Throughput, "http://127.0.0.1:8080/", 30*time.Second, 2), " ")

	want := "--no-tui --output-format json --http-version 1.1 --disable-compression --worker-threads 2 -c 64 -z 30s -w http://127.0.0.1:8080/"
	if got != want {
		t.Errorf("oha %s\nwant oha %s", got, want)
	}
}

func TestFixedRateLatencyHoldsTheRateAndCorrectsCoordinatedOmission(t *testing.T) {
	got := strings.Join(WorkloadArguments(plan.FixedRateLatency, "http://127.0.0.1:8080/", 5*time.Second, 2), " ")

	want := "--no-tui --output-format json --http-version 1.1 --disable-compression --worker-threads 2 -c 64 -z 5s -w -q 5000 --latency-correction http://127.0.0.1:8080/"
	if got != want {
		t.Errorf("oha %s\nwant oha %s", got, want)
	}
}

func TestReadinessSendsOneBoundedRequest(t *testing.T) {
	got := strings.Join(ReadinessArguments("http://127.0.0.1:9001/"), " ")

	want := "--no-tui --output-format json --http-version 1.1 --disable-compression -n 1 -c 1 -t 2s --connect-timeout 2s http://127.0.0.1:9001/"
	if got != want {
		t.Errorf("oha %s\nwant oha %s", got, want)
	}
}

func TestCleanRunReportsItsNumbersAndNoProblems(t *testing.T) {
	result := parseFixture(t, "throughput.json")

	if result.RequestsPerSecond != 104434.92427231408 {
		t.Errorf("req/s = %v, want 104434.92427231408", result.RequestsPerSecond)
	}
	if result.P50 != 587019*time.Nanosecond || result.P99 != 1061098*time.Nanosecond || result.P999 != 3802570*time.Nanosecond {
		t.Errorf("p50 %v, p99 %v, p99.9 %v, want 587.019µs, 1.061098ms, 3.80257ms", result.P50, result.P99, result.P999)
	}
	if problems := result.Problems(); len(problems) != 0 {
		t.Errorf("problems = %q, want none", problems)
	}
}

func TestRefusedConnectionIsAProblem(t *testing.T) {
	problems := parseFixture(t, "connection-refused.json").Problems()

	want := "1 request failed: Connection refused (os error 111)"
	if strings.Join(problems, "; ") != want {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

func TestAnswerOtherThan2xxIsAProblem(t *testing.T) {
	problems := parseFixture(t, "no-alive-backends.json").Problems()

	want := "answered 503 to 51200 requests"
	if strings.Join(problems, "; ") != want {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

func TestOutputThatIsNotOhaJSONIsAnError(t *testing.T) {
	if _, err := ParseResult([]byte(`{"something": "else"}`)); err == nil {
		t.Error("ParseResult accepted JSON without oha's summary")
	}
	if _, err := ParseResult([]byte("Summary:\n  Success rate: 100.00%")); err == nil {
		t.Error("ParseResult accepted oha's text output")
	}
}

func parseFixture(t *testing.T, name string) Result {
	t.Helper()
	output, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseResult(output)
	if err != nil {
		t.Fatalf("ParseResult(%s): %v", name, err)
	}
	return result
}
