package report

import (
	"testing"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

func TestPublishVerdict(t *testing.T) {
	requestedByTheTagMessage := plan.PublishRequest{Requested: true, Reason: "the v1.2.0 tag message has the line `benchmark: publish`"}
	tests := []struct {
		name        string
		publish     plan.PublishRequest
		workloads   []WorkloadResults
		wantPublish bool
		wantLine    string
	}{
		{"requested, every row ok", requestedByTheTagMessage, []WorkloadResults{throughputWith(okRow("divisor"), okRow("divisor (Baseline)"))},
			true, "**Release page:** publishing this section to the v1.2.0 release, because the v1.2.0 tag message has the line `benchmark: publish`.\n"},
		{"requested, a row failed", requestedByTheTagMessage, []WorkloadResults{
			throughputWith(okRow("divisor"), failedRow("divisor (Baseline)")),
			fixedRateLatencyWith(okRow("divisor"), failedRow("divisor (Baseline)")),
		}, false, "**Release page:** not published, because these rows failed: divisor (Baseline) in Throughput, divisor (Baseline) in Fixed-rate latency.\n"},
		{"not requested", plan.PublishRequest{Reason: "publish was not ticked for this manual run"}, []WorkloadResults{throughputWith(okRow("divisor"))},
			false, "**Release page:** not published, because publish was not ticked for this manual run.\n"},
		{"not requested, a row failed", plan.PublishRequest{Reason: "a local run never publishes"}, []WorkloadResults{throughputWith(failedRow("divisor"))},
			false, "**Release page:** not published, because a local run never publishes.\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			built := Report{Plan: plan.Plan{Ref: "v1.2.0", Publish: test.publish}, Workloads: test.workloads}

			verdict := built.PublishVerdict()

			if verdict.Publish != test.wantPublish {
				t.Errorf("publish = %v, want %v", verdict.Publish, test.wantPublish)
			}
			if got := verdict.SummaryLine(); got != test.wantLine {
				t.Errorf("summary line:\n%s\nwant:\n%s", got, test.wantLine)
			}
		})
	}
}

func TestSkippedRunIsNotPublished(t *testing.T) {
	skipped := plan.Plan{Ref: "v1.3.0-rc.1", SkipReason: "v1.3.0-rc.1 is a prerelease tag, ...",
		Publish: plan.PublishRequest{Reason: "v1.3.0-rc.1 is a prerelease tag, which a release-triggered run skips"}}

	verdict := SkippedRunPublishVerdict(&skipped)

	want := "**Release page:** not published, because v1.3.0-rc.1 is a prerelease tag, which a release-triggered run skips.\n"
	if verdict.Publish || verdict.SummaryLine() != want {
		t.Errorf("verdict = %+v with summary line %q, want not published: %q", verdict, verdict.SummaryLine(), want)
	}
}

func throughputWith(rows ...Row) WorkloadResults {
	return WorkloadResults{Workload: plan.Throughput, Rows: rows}
}

func fixedRateLatencyWith(rows ...Row) WorkloadResults {
	return WorkloadResults{Workload: plan.FixedRateLatency, Rows: rows}
}

func okRow(contenderName string) Row {
	return Row{Contender: plan.Contender{Name: contenderName}, Rounds: []RoundResult{{Round: 1, Numbers: Numbers{RequestsPerSecond: 20000}}}}
}

func failedRow(contenderName string) Row {
	return Row{Contender: plan.Contender{Name: contenderName}, Rounds: []RoundResult{{Round: 1, Failure: "not ready within 30s"}}}
}
