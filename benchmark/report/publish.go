package report

import (
	"strings"

	"github.com/aaydin-tr/divisor/benchmark/plan"
)

// PublishVerdict is whether the section goes on the ref's release page:
// only when the maintainer asked for it and no row failed.
type PublishVerdict struct {
	Publish bool
	Ref     string
	Reason  string
}

func (r *Report) PublishVerdict() PublishVerdict {
	request := r.Plan.Publish
	if !request.Requested {
		return PublishVerdict{Ref: r.Plan.Ref, Reason: request.Reason}
	}
	if failedRows := r.FailedRows(); len(failedRows) > 0 {
		return PublishVerdict{Ref: r.Plan.Ref, Reason: "these rows failed: " + strings.Join(failedRows, ", ")}
	}
	return PublishVerdict{Publish: true, Ref: r.Plan.Ref, Reason: request.Reason}
}

// SkippedRunPublishVerdict never publishes: a skipped run measured nothing.
func SkippedRunPublishVerdict(skipped *plan.Plan) PublishVerdict {
	return PublishVerdict{Ref: skipped.Ref, Reason: skipped.Publish.Reason}
}

// FailedRows names each failed row as "<Contender> in <Workload>".
func (r *Report) FailedRows() []string {
	var failed []string
	for _, results := range r.Workloads {
		for i := range results.Rows {
			if results.Rows[i].Failed() {
				failed = append(failed, results.Rows[i].Contender.Name+" in "+results.Workload.Name)
			}
		}
	}
	return failed
}

// SummaryLine closes the run summary, below the section it is about.
func (v PublishVerdict) SummaryLine() string {
	if v.Publish {
		return "**Release page:** publishing this section to the " + v.Ref + " release, because " + v.Reason + ".\n"
	}
	return "**Release page:** not published, because " + v.Reason + ".\n"
}
