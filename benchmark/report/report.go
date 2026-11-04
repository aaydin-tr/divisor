// Package report turns a recording into the numbers a reader sees, and renders them as the section
// and benchmark.json.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/oha"
	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

type Report struct {
	Plan        plan.Plan
	Environment recording.Environment
	Workloads   []WorkloadResults

	BaselineComparison BaselineComparison
}

type WorkloadResults struct {
	Workload plan.Workload
	// Rows follow the Plan's Contender order, the fixed row order.
	Rows []Row
}

type Row struct {
	Contender plan.Contender
	Rounds    []RoundResult
	// Median and Spread are zero for a failed row.
	Median Numbers
	Spread Spread
}

type Numbers struct {
	RequestsPerSecond float64
	P50Milliseconds   float64
	P99Milliseconds   float64
	P999Milliseconds  float64
}

// Spread is (max − min) / median over the rounds, a fraction per number.
type Spread struct {
	RequestsPerSecond float64
	P50               float64
	P99               float64
	P999              float64
}

type RoundResult struct {
	Round   int
	Numbers Numbers
	// Failure is why this round does not count; a row with one failed round fails.
	Failure string
}

func (r *Row) Failed() bool {
	return len(r.Failures()) > 0
}

func (r *Row) Failures() []string {
	var failures []string
	for _, round := range r.Rounds {
		if round.Failure != "" {
			failures = append(failures, fmt.Sprintf("round %d: %s", round.Round, round.Failure))
		}
	}
	return failures
}

func Build(recorded *recording.Recording) (*Report, error) {
	built := &Report{Plan: recorded.Plan, Environment: recorded.Environment}
	for _, workload := range recorded.Plan.Workloads {
		results := WorkloadResults{Workload: workload}
		for i := range recorded.Plan.Contenders {
			row, err := buildRow(recorded, &recorded.Plan.Contenders[i], workload)
			if err != nil {
				return nil, err
			}
			results.Rows = append(results.Rows, row)
		}
		built.Workloads = append(built.Workloads, results)
	}
	built.BaselineComparison = compareWithBaseline(built)
	return built, nil
}

func buildRow(recorded *recording.Recording, contender *plan.Contender, workload plan.Workload) (Row, error) {
	row := Row{Contender: *contender}
	for round := 1; round <= recorded.Plan.Schedule.Rounds; round++ {
		roundResult, err := buildRoundResult(recorded, round, contender, workload)
		if err != nil {
			return Row{}, err
		}
		row.Rounds = append(row.Rounds, roundResult)
	}
	if !row.Failed() {
		row.Median, row.Spread = medianAndSpread(row.Rounds)
	}
	return row, nil
}

func buildRoundResult(recorded *recording.Recording, round int, contender *plan.Contender, workload plan.Workload) (RoundResult, error) {
	measurement, found := findMeasurement(recorded.Measurements, round, contender.ID, workload.ID)
	switch {
	case !found:
		return RoundResult{Round: round, Failure: "not measured"}, nil
	case measurement.Failure != "":
		return RoundResult{Round: round, Failure: measurement.Failure}, nil
	}

	result, err := oha.ParseResult(measurement.OhaOutput)
	if err != nil {
		return RoundResult{}, fmt.Errorf("%s: %w", measurement.OhaOutputFile, err)
	}
	if problems := result.Problems(); len(problems) > 0 {
		return RoundResult{Round: round, Failure: strings.Join(problems, "; ")}, nil
	}
	return RoundResult{Round: round, Numbers: Numbers{
		RequestsPerSecond: result.RequestsPerSecond,
		P50Milliseconds:   millisecondsOf(result.P50),
		P99Milliseconds:   millisecondsOf(result.P99),
		P999Milliseconds:  millisecondsOf(result.P999),
	}}, nil
}

func millisecondsOf(latency time.Duration) float64 {
	return float64(latency) / float64(time.Millisecond)
}

func findMeasurement(measurements []recording.Measurement, round int, contenderID, workloadID string) (*recording.Measurement, bool) {
	for i := range measurements {
		if measurements[i].Round == round && measurements[i].ContenderID == contenderID && measurements[i].WorkloadID == workloadID {
			return &measurements[i], true
		}
	}
	return nil, false
}

func medianAndSpread(rounds []RoundResult) (median Numbers, spread Spread) {
	numbersOf := func(number func(Numbers) float64) []float64 {
		values := make([]float64, 0, len(rounds))
		for _, round := range rounds {
			values = append(values, number(round.Numbers))
		}
		return values
	}
	median.RequestsPerSecond, spread.RequestsPerSecond = medianAndSpreadOf(numbersOf(func(numbers Numbers) float64 { return numbers.RequestsPerSecond }))
	median.P50Milliseconds, spread.P50 = medianAndSpreadOf(numbersOf(func(numbers Numbers) float64 { return numbers.P50Milliseconds }))
	median.P99Milliseconds, spread.P99 = medianAndSpreadOf(numbersOf(func(numbers Numbers) float64 { return numbers.P99Milliseconds }))
	median.P999Milliseconds, spread.P999 = medianAndSpreadOf(numbersOf(func(numbers Numbers) float64 { return numbers.P999Milliseconds }))
	return median, spread
}

func medianAndSpreadOf(values []float64) (median, spread float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	median = sorted[middle]
	if len(sorted)%2 == 0 {
		median = (sorted[middle-1] + sorted[middle]) / 2
	}
	if median == 0 {
		return 0, 0
	}
	return median, (sorted[len(sorted)-1] - sorted[0]) / median
}
