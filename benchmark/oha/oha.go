// Package oha is what the benchmark relies on from the oha load generator: its flags and JSON fields.
package oha

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

var commonArguments = []string{"--no-tui", "--output-format", "json", "--http-version", "1.1", "--disable-compression"}

// WorkloadArguments waits for requests in flight at the deadline (-w):
// without it oha aborts them and counts them as errors.
func WorkloadArguments(workload plan.Workload, url string, duration time.Duration, workerThreads int) []string {
	arguments := append([]string{}, commonArguments...)
	arguments = append(arguments,
		"--worker-threads", strconv.Itoa(workerThreads),
		"-c", strconv.Itoa(workload.Connections),
		"-z", fmt.Sprintf("%ds", int(duration.Seconds())),
		"-w",
	)
	if workload.RequestsPerSecond > 0 {
		arguments = append(arguments, "-q", strconv.Itoa(workload.RequestsPerSecond))
	}
	if workload.CorrectCoordinatedOmission {
		arguments = append(arguments, "--latency-correction")
	}
	return append(arguments, url)
}

func ReadinessArguments(url string) []string {
	arguments := append([]string{}, commonArguments...)
	return append(arguments, "-n", "1", "-c", "1", "-t", "2s", "--connect-timeout", "2s", url)
}

type Result struct {
	RequestsPerSecond float64
	P50, P99, P999    time.Duration
	StatusCodes       map[int]int
	Errors            map[string]int
}

type outputJSON struct {
	Summary *struct {
		RequestsPerSec float64 `json:"requestsPerSec"`
	} `json:"summary"`
	LatencyPercentiles     map[string]*float64 `json:"latencyPercentiles"`
	StatusCodeDistribution map[string]int      `json:"statusCodeDistribution"`
	ErrorDistribution      map[string]int      `json:"errorDistribution"`
}

var errNotOhaJSON = errors.New("not oha's JSON output: no summary")

func ParseResult(output []byte) (Result, error) {
	var parsed outputJSON
	if err := json.Unmarshal(output, &parsed); err != nil {
		return Result{}, fmt.Errorf("reading oha's JSON output: %w", err)
	}
	if parsed.Summary == nil {
		return Result{}, errNotOhaJSON
	}

	statusCodes := make(map[int]int, len(parsed.StatusCodeDistribution))
	for status, count := range parsed.StatusCodeDistribution {
		code, err := strconv.Atoi(status)
		if err != nil {
			return Result{}, fmt.Errorf("oha's statusCodeDistribution has a non-numeric status %q", status)
		}
		statusCodes[code] = count
	}

	return Result{
		RequestsPerSecond: parsed.Summary.RequestsPerSec,
		P50:               percentile(parsed.LatencyPercentiles, "p50"),
		P99:               percentile(parsed.LatencyPercentiles, "p99"),
		P999:              percentile(parsed.LatencyPercentiles, "p99.9"),
		StatusCodes:       statusCodes,
		Errors:            parsed.ErrorDistribution,
	}, nil
}

// percentile is zero when oha had no answered request to measure (JSON null).
func percentile(percentiles map[string]*float64, name string) time.Duration {
	seconds := percentiles[name]
	if seconds == nil {
		return 0
	}
	return time.Duration(math.Round(*seconds * float64(time.Second)))
}

// Problems are why the run does not count: a non-2xx answer, a failed request, or no request at all.
func (r Result) Problems() []string {
	var problems []string
	answered := 0
	for _, status := range sortedKeys(r.StatusCodes) {
		answered += r.StatusCodes[status]
		if status < 200 || status > 299 {
			problems = append(problems, fmt.Sprintf("answered %d to %s", status, requests(r.StatusCodes[status])))
		}
	}
	for _, failure := range sortedKeys(r.Errors) {
		problems = append(problems, fmt.Sprintf("%s failed: %s", requests(r.Errors[failure]), failure))
	}
	if answered == 0 && len(r.Errors) == 0 {
		problems = append(problems, "sent no requests")
	}
	return problems
}

func requests(count int) string {
	if count == 1 {
		return "1 request"
	}
	return strconv.Itoa(count) + " requests"
}

func sortedKeys[K int | string](counts map[K]int) []K {
	keys := make([]K, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
