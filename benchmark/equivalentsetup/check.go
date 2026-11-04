// Package equivalentsetup checks, from what the Benchmark backends answered, that a Contender is in
// the Equivalent setup.
package equivalentsetup

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/backend"
	"github.com/aaydin-tr/divisor/benchmark/oha"
	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

const (
	only2xxAnswers              = "only 2xx answers"
	everyRequestHasForwardedFor = "every request arrives with X-Forwarded-For"
	keepAliveToBackends         = "keep-alive to the Benchmark backends"
	contenderBecameReady        = "ready"

	// evenSpreadTolerance is how far a Benchmark backend's share may stray
	// from an even one; round-robin keeps it near zero.
	evenSpreadTolerance = 0.2
	// requestsPerBackendConnection is the fewest requests each Backend
	// connection must carry on average to count as kept alive.
	requestsPerBackendConnection = 10
)

// Sample is what came back for every request the sampler sent through a Contender.
type Sample struct {
	Answers []Answer `json:"answers"`
	// Errors counts requests that got no answer, by error.
	Errors map[string]int `json:"errors,omitempty"`
}

// Answer carries the Benchmark backend's headers as the Contender returned them.
type Answer struct {
	Status       int    `json:"status"`
	AnsweredBy   string `json:"answered_by"`
	ForwardedFor string `json:"forwarded_for"`
}

// Verify checks every Contender of a recording that checks the Equivalent
// setup, and returns each failed check as "<Contender>: <check>: <why>".
func Verify(recorded *recording.Recording) ([]string, error) {
	var failures []string
	for _, contender := range recorded.Plan.Contenders {
		failedChecks, err := failedChecksOf(recorded, &contender)
		if err != nil {
			return nil, err
		}
		for _, failedCheck := range failedChecks {
			failures = append(failures, contender.Name+": "+failedCheck)
		}
	}
	return failures, nil
}

func failedChecksOf(recorded *recording.Recording, contender *plan.Contender) ([]string, error) {
	var failedChecks []string
	for round := 1; round <= recorded.Plan.Schedule.Rounds; round++ {
		sample, found, err := findSample(recorded, round, contender.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			failedChecks = append(failedChecks, contenderBecameReady+": "+notReadyReason(recorded, round, contender.ID))
			continue
		}
		failedChecks = append(failedChecks, Check(sample, recorded.Plan.Network.BenchmarkBackends)...)
	}

	for _, workload := range recorded.Plan.Workloads {
		for _, measurement := range recorded.Measurements {
			if measurement.ContenderID != contender.ID || measurement.WorkloadID != workload.ID || measurement.OhaOutput == nil {
				continue
			}
			result, err := oha.ParseResult(measurement.OhaOutput)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", measurement.OhaOutputFile, err)
			}
			if problems := result.Problems(); len(problems) > 0 {
				failedChecks = append(failedChecks, only2xxAnswers+": "+workload.Name+": "+strings.Join(problems, "; "))
			}
		}
	}
	return failedChecks, nil
}

func findSample(recorded *recording.Recording, round int, contenderID string) (Sample, bool, error) {
	for _, recordedSample := range recorded.EquivalentSetupSamples {
		if recordedSample.Round != round || recordedSample.ContenderID != contenderID {
			continue
		}
		var sample Sample
		if err := json.Unmarshal(recordedSample.Sample, &sample); err != nil {
			return Sample{}, false, fmt.Errorf("reading the Equivalent setup sample %s: %w", recordedSample.SampleFile, err)
		}
		return sample, true, nil
	}
	return Sample{}, false, nil
}

func notReadyReason(recorded *recording.Recording, round int, contenderID string) string {
	for _, measurement := range recorded.Measurements {
		if measurement.Round == round && measurement.ContenderID == contenderID && measurement.Failure != "" {
			return measurement.Failure
		}
	}
	return "the Equivalent setup was not sampled"
}

// Check returns each check the sample fails, as "<check>: <why>".
func Check(sample Sample, benchmarkBackends []plan.BenchmarkBackend) []string {
	failedChecks := only2xxAnswersFailures(sample)
	if len(sample.Answers) == 0 {
		return failedChecks
	}
	answersByBackend, answersFromNoBenchmarkBackend := groupByBenchmarkBackend(sample.Answers, benchmarkBackends)
	failedChecks = append(failedChecks, evenSpreadFailures(benchmarkBackends, answersByBackend, answersFromNoBenchmarkBackend, len(sample.Answers))...)
	failedChecks = append(failedChecks, forwardedForFailures(answersByBackend)...)
	return append(failedChecks, keepAliveFailures(benchmarkBackends, answersByBackend)...)
}

func only2xxAnswersFailures(sample Sample) []string {
	statusCodes := map[int]int{}
	for _, answer := range sample.Answers {
		statusCodes[answer.Status]++
	}
	problems := oha.Result{StatusCodes: statusCodes, Errors: sample.Errors}.Problems()
	failedChecks := make([]string, 0, len(problems))
	for _, problem := range problems {
		failedChecks = append(failedChecks, only2xxAnswers+": "+problem)
	}
	return failedChecks
}

func groupByBenchmarkBackend(answers []Answer, benchmarkBackends []plan.BenchmarkBackend) (answersByBackend map[string][]Answer, answersFromNoBenchmarkBackend int) {
	answersByBackend = make(map[string][]Answer, len(benchmarkBackends))
	for _, benchmarkBackend := range benchmarkBackends {
		answersByBackend[benchmarkBackend.Name] = nil
	}
	for _, answer := range answers {
		backendName, _, _ := strings.Cut(answer.AnsweredBy, "/")
		if _, isBenchmarkBackend := answersByBackend[backendName]; !isBenchmarkBackend {
			answersFromNoBenchmarkBackend++
			continue
		}
		answersByBackend[backendName] = append(answersByBackend[backendName], answer)
	}
	return answersByBackend, answersFromNoBenchmarkBackend
}

func evenSpreadFailures(benchmarkBackends []plan.BenchmarkBackend, answersByBackend map[string][]Answer, answersFromNoBenchmarkBackend, answerCount int) []string {
	evenSpread := fmt.Sprintf("an even spread over the %d Benchmark backends", len(benchmarkBackends))
	var failedChecks []string
	if answersFromNoBenchmarkBackend > 0 {
		failedChecks = append(failedChecks, fmt.Sprintf("%s: %d of %d answers came from no Benchmark backend", evenSpread, answersFromNoBenchmarkBackend, answerCount))
	}

	evenShare := float64(answerCount) / float64(len(benchmarkBackends))
	fewestAnswers := int(evenShare * (1 - evenSpreadTolerance))
	mostAnswers := int(evenShare * (1 + evenSpreadTolerance))
	unevenShare := false
	shares := make([]string, 0, len(benchmarkBackends))
	for _, benchmarkBackend := range benchmarkBackends {
		answered := len(answersByBackend[benchmarkBackend.Name])
		unevenShare = unevenShare || answered < fewestAnswers || answered > mostAnswers
		shares = append(shares, fmt.Sprintf("%s answered %d", benchmarkBackend.Name, answered))
	}
	if unevenShare {
		failedChecks = append(failedChecks, fmt.Sprintf("%s: want each to answer %d to %d of %d requests: %s",
			evenSpread, fewestAnswers, mostAnswers, answerCount, strings.Join(shares, ", ")))
	}
	return failedChecks
}

func forwardedForFailures(answersByBackend map[string][]Answer) []string {
	withoutForwardedFor, backendAnswers := 0, 0
	for _, answers := range answersByBackend {
		backendAnswers += len(answers)
		for _, answer := range answers {
			if answer.ForwardedFor != backend.ForwardedForPresent {
				withoutForwardedFor++
			}
		}
	}
	if withoutForwardedFor == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s: %d of %d requests arrived without it", everyRequestHasForwardedFor, withoutForwardedFor, backendAnswers)}
}

func keepAliveFailures(benchmarkBackends []plan.BenchmarkBackend, answersByBackend map[string][]Answer) []string {
	var failedChecks []string
	for _, benchmarkBackend := range benchmarkBackends {
		answers := answersByBackend[benchmarkBackend.Name]
		connectionIDs := map[string]bool{}
		for _, answer := range answers {
			connectionIDs[answer.AnsweredBy] = true
		}
		mostConnections := max(1, len(answers)/requestsPerBackendConnection)
		if len(connectionIDs) > mostConnections {
			failedChecks = append(failedChecks, fmt.Sprintf("%s: %s answered %d requests over %d connections, want at most %d",
				keepAliveToBackends, benchmarkBackend.Name, len(answers), len(connectionIDs), mostConnections))
		}
	}
	return failedChecks
}
