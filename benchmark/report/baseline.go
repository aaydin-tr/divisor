package report

import (
	"fmt"
	"math"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

// NoiseFloor is the smallest change against the Baseline release that can
// mean anything on a shared runner, whatever the rounds' spread.
const NoiseFloor = 0.03

type Verdict string

const (
	Improvement Verdict = "improvement"
	Regression  Verdict = "regression"
	WithinNoise Verdict = "within_noise"
	NotCompared Verdict = "not_compared"
)

// BaselineComparison is what changed between the divisor being benchmarked
// and its Baseline release in this run; no Changes when there is no Baseline.
type BaselineComparison struct {
	BaselineRelease plan.BaselineRelease
	Changes         []Change
}

type Change struct {
	Workload plan.Workload
	Number   ComparedNumber
	// Fraction is (divisor − Baseline) / Baseline.
	Fraction float64
	// NoiseThreshold is the larger of NoiseFloor and both rows' spreads of Number.
	NoiseThreshold    float64
	Verdict           Verdict
	NotComparedReason string
}

type ComparedNumber struct {
	WorkloadID    string
	Name          string
	JSONName      string
	valueOf       func(Numbers) float64
	spreadOf      func(Spread) float64
	lowerIsBetter bool
}

var comparedNumbers = []ComparedNumber{
	{WorkloadID: plan.Throughput.ID, Name: "req/s", JSONName: "requests_per_second",
		valueOf: func(n Numbers) float64 { return n.RequestsPerSecond }, spreadOf: func(s Spread) float64 { return s.RequestsPerSecond }},
	{WorkloadID: plan.FixedRateLatency.ID, Name: "p99", JSONName: "p99_ms",
		valueOf: func(n Numbers) float64 { return n.P99Milliseconds }, spreadOf: func(s Spread) float64 { return s.P99 }, lowerIsBetter: true},
}

func compareWithBaseline(r *Report) BaselineComparison {
	comparison := BaselineComparison{BaselineRelease: r.Plan.BaselineRelease}
	if !comparison.BaselineRelease.Exists() {
		return comparison
	}
	for _, number := range comparedNumbers {
		for i := range r.Workloads {
			if results := &r.Workloads[i]; results.Workload.ID == number.WorkloadID {
				comparison.Changes = append(comparison.Changes, changeAgainstBaseline(&r.Plan, results, number))
			}
		}
	}
	return comparison
}

func smokeRunNotComparedReason(schedule plan.Schedule) string {
	return fmt.Sprintf("a smoke run's %ds numbers only check the Equivalent setup", schedule.MeasurementSeconds)
}

func changeAgainstBaseline(measured *plan.Plan, results *WorkloadResults, number ComparedNumber) Change {
	change := Change{Workload: results.Workload, Number: number, Verdict: NotCompared}
	if measured.Mode == plan.SmokeMode {
		change.NotComparedReason = smokeRunNotComparedReason(measured.Schedule)
		return change
	}
	divisorRow, baselineRow := rowOf(results, plan.DivisorContenderID), rowOf(results, plan.BaselineContenderID)

	var failedRows []string
	for _, row := range []*Row{divisorRow, baselineRow} {
		if row.Failed() {
			failedRows = append(failedRows, row.Contender.Name)
		}
	}
	if len(failedRows) > 0 {
		change.NotComparedReason = strings.Join(failedRows, " and ") + " failed"
		return change
	}

	baselineValue := number.valueOf(baselineRow.Median)
	if baselineValue == 0 {
		change.NotComparedReason = baselineRow.Contender.Name + " measured 0"
		return change
	}
	change.Fraction = (number.valueOf(divisorRow.Median) - baselineValue) / baselineValue
	change.NoiseThreshold = max(NoiseFloor, number.spreadOf(divisorRow.Spread), number.spreadOf(baselineRow.Spread))

	improved := change.Fraction > 0
	if number.lowerIsBetter {
		improved = change.Fraction < 0
	}
	switch {
	case math.Abs(change.Fraction) < change.NoiseThreshold:
		change.Verdict = WithinNoise
	case improved:
		change.Verdict = Improvement
	default:
		change.Verdict = Regression
	}
	return change
}

// rowOf counts a Contender missing from the Workload as a failed row.
func rowOf(results *WorkloadResults, contenderID string) *Row {
	for i := range results.Rows {
		if results.Rows[i].Contender.ID == contenderID {
			return &results.Rows[i]
		}
	}
	return &Row{Contender: plan.Contender{ID: contenderID, Name: contenderID}, Rounds: []RoundResult{{Failure: "not in the recording"}}}
}
