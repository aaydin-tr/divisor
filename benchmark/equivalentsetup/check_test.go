package equivalentsetup

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/benchmark/plan"
	"github.com/aaydin-tr/divisor/benchmark/recording"
)

const requestsPerBackend = 500

var benchmarkBackendNames = []string{"backend-1", "backend-2", "backend-3", "backend-4"}

// roundRobinOverKeepAliveConnections is what a Contender in the Equivalent
// setup leaves: each Backend answers its share over 8 reused connections.
func roundRobinOverKeepAliveConnections() Sample {
	var sample Sample
	for i := range requestsPerBackend * len(benchmarkBackendNames) {
		backendName := benchmarkBackendNames[i%len(benchmarkBackendNames)]
		sample.Answers = append(sample.Answers, Answer{
			Status:       200,
			AnsweredBy:   fmt.Sprintf("%s/%d", backendName, i%8),
			ForwardedFor: "present",
		})
	}
	return sample
}

func TestEquivalentSetupChecks(t *testing.T) {
	tests := []struct {
		name   string
		sample func() Sample
		want   []string
	}{
		{
			name:   "round-robin over keep-alive connections with X-Forwarded-For",
			sample: roundRobinOverKeepAliveConnections,
		},
		{
			name: "a Backend answered an error",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				sample.Answers[3].Status = 502
				return sample
			},
			want: []string{"only 2xx answers: answered 502 to 1 request"},
		},
		{
			name: "requests that got no answer",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				sample.Errors = map[string]int{"the server closed the connection before returning the first response byte": 2}
				return sample
			},
			want: []string{"only 2xx answers: 2 requests failed: the server closed the connection before returning the first response byte"},
		},
		{
			name:   "no request at all",
			sample: func() Sample { return Sample{} },
			want:   []string{"only 2xx answers: sent no requests"},
		},
		{
			name: "one Benchmark backend left out of the config",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				for i := range sample.Answers {
					if strings.HasPrefix(sample.Answers[i].AnsweredBy, "backend-4/") {
						sample.Answers[i].AnsweredBy = fmt.Sprintf("backend-%d/%d", i%3+1, i%8)
					}
				}
				return sample
			},
			want: []string{"an even spread over the 4 Benchmark backends: want each to answer 400 to 600 of 2000 requests: backend-1 answered 667, backend-2 answered 667, backend-3 answered 666, backend-4 answered 0"},
		},
		{
			name: "answers no Benchmark backend gave",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				sample.Answers[0].AnsweredBy = ""
				sample.Answers[4].AnsweredBy = ""
				return sample
			},
			want: []string{"an even spread over the 4 Benchmark backends: 2 of 2000 answers came from no Benchmark backend"},
		},
		{
			name: "X-Forwarded-For not set",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				for i := range sample.Answers {
					sample.Answers[i].ForwardedFor = "absent"
				}
				return sample
			},
			want: []string{"every request arrives with X-Forwarded-For: 2000 of 2000 requests arrived without it"},
		},
		{
			name: "a new Backend connection for every request",
			sample: func() Sample {
				sample := roundRobinOverKeepAliveConnections()
				for i := range sample.Answers {
					sample.Answers[i].AnsweredBy = fmt.Sprintf("%s/%d", benchmarkBackendNames[i%4], i)
				}
				return sample
			},
			want: []string{
				"keep-alive to the Benchmark backends: backend-1 answered 500 requests over 500 connections, want at most 50",
				"keep-alive to the Benchmark backends: backend-2 answered 500 requests over 500 connections, want at most 50",
				"keep-alive to the Benchmark backends: backend-3 answered 500 requests over 500 connections, want at most 50",
				"keep-alive to the Benchmark backends: backend-4 answered 500 requests over 500 connections, want at most 50",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Check(test.sample(), benchmarkBackends())
			if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Errorf("failed checks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(test.want, "\n"))
			}
		})
	}
}

func TestVerifyNamesTheContenderOfEveryFailedCheck(t *testing.T) {
	recorded := smokeRecording(t, "divisor", "nginx")
	inEquivalentSetup := roundRobinOverKeepAliveConnections()
	withoutForwardedFor := roundRobinOverKeepAliveConnections()
	withoutForwardedFor.Answers[0].ForwardedFor = "absent"
	recordSample(t, recorded, "divisor", inEquivalentSetup)
	recordSample(t, recorded, "nginx", withoutForwardedFor)

	got := verify(t, recorded)

	want := "nginx: every request arrives with X-Forwarded-For: 1 of 2000 requests arrived without it"
	if strings.Join(got, "\n") != want {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestVerifyFailsAContenderThatNeverBecameReady(t *testing.T) {
	recorded := smokeRecording(t, "divisor")
	for _, workload := range recorded.Plan.Workloads {
		recorded.Measurements = append(recorded.Measurements, recording.Measurement{
			Round: 1, ContenderID: "divisor", WorkloadID: workload.ID,
			Failure: "not ready within 30s: answered 503 to 1 request",
		})
	}

	got := verify(t, recorded)

	want := "divisor: ready: not ready within 30s: answered 503 to 1 request"
	if strings.Join(got, "\n") != want {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestVerifyFailsAWorkloadThatGotAnErrorAnswer(t *testing.T) {
	recorded := smokeRecording(t, "divisor")
	recordSample(t, recorded, "divisor", roundRobinOverKeepAliveConnections())
	recorded.Measurements = []recording.Measurement{
		{Round: 1, ContenderID: "divisor", WorkloadID: plan.Throughput.ID, OhaOutput: ohaOutput(map[string]int{"200": 9000, "502": 12})},
		{Round: 1, ContenderID: "divisor", WorkloadID: plan.FixedRateLatency.ID, OhaOutput: ohaOutput(map[string]int{"200": 15000})},
	}

	got := verify(t, recorded)

	want := "divisor: only 2xx answers: Throughput: answered 502 to 12 requests"
	if strings.Join(got, "\n") != want {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func benchmarkBackends() []plan.BenchmarkBackend {
	decided, _ := plan.Decide(&plan.Inputs{Trigger: plan.SmokeTrigger})
	return decided.Network.BenchmarkBackends
}

func smokeRecording(t *testing.T, contenderIDs ...string) *recording.Recording {
	t.Helper()
	decided, err := plan.Decide(&plan.Inputs{Trigger: plan.SmokeTrigger})
	if err != nil {
		t.Fatal(err)
	}
	decided.Contenders = nil
	for _, id := range contenderIDs {
		decided.Contenders = append(decided.Contenders, plan.Contender{ID: id, Name: id})
	}
	return &recording.Recording{Plan: decided}
}

func recordSample(t *testing.T, recorded *recording.Recording, contenderID string, sample Sample) {
	t.Helper()
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	recorded.EquivalentSetupSamples = append(recorded.EquivalentSetupSamples, recording.EquivalentSetupSample{
		Round: 1, ContenderID: contenderID, Sample: encoded,
	})
}

func ohaOutput(statusCodes map[string]int) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"summary":                map[string]any{"requestsPerSec": 3000.0},
		"latencyPercentiles":     map[string]any{"p50": 0.001, "p99": 0.002, "p99.9": 0.003},
		"statusCodeDistribution": statusCodes,
		"errorDistribution":      map[string]int{},
	})
	return encoded
}

func verify(t *testing.T, recorded *recording.Recording) []string {
	t.Helper()
	failures, err := Verify(recorded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return failures
}
