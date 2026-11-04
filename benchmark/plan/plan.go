// Package plan decides what a Release benchmark run measures. It is pure: every input arrives in Inputs.
package plan

type Mode string

const FullMode Mode = "full"

type Inputs struct {
	Trigger Trigger
	// Ref is the branch or tag the checkout is on; empty for a detached checkout.
	Ref                           string
	Commit                        string
	CheckoutHasUncommittedChanges bool

	// Tags are the repository's tags, release tags or not.
	Tags []string
	// TagMessage is the annotated tag's message; empty for a lightweight tag or a branch.
	TagMessage string
	// BenchmarkCommit is the commit the benchmark module (Contender definitions
	// and method) came from; a measured tag is usually older.
	BenchmarkCommit string

	PublishTicked bool
	// RefHasGitHubRelease is looked up only when PublishTicked is set.
	RefHasGitHubRelease bool
}

type Plan struct {
	Trigger         Trigger `json:"trigger"`
	Mode            Mode    `json:"mode"`
	Ref             string  `json:"ref"`
	Commit          string  `json:"commit"`
	BenchmarkCommit string  `json:"benchmark_commit,omitempty"`
	// SkipReason is set when the run measures nothing, and says why.
	SkipReason string      `json:"skip_reason,omitempty"`
	Contenders []Contender `json:"contenders"`
	Workloads  []Workload  `json:"workloads"`
	Schedule   Schedule    `json:"schedule"`
	Resources  Resources   `json:"resources"`
	Network    Network     `json:"network"`

	// BaselineRelease is also a Contender when it exists.
	BaselineRelease BaselineRelease `json:"baseline_release"`

	Publish PublishRequest `json:"publish"`
}

func Decide(inputs *Inputs) (Plan, error) {
	if err := checkTrigger(inputs); err != nil {
		return Plan{}, err
	}
	publish, err := publishRequestFor(inputs)
	if err != nil {
		return Plan{}, err
	}
	if reason := skipReason(inputs); reason != "" {
		return Plan{Trigger: inputs.Trigger, Ref: inputs.Ref, Commit: inputs.Commit, BenchmarkCommit: inputs.BenchmarkCommit, SkipReason: reason, Publish: publish}, nil
	}
	decidedContenders, err := contendersFor(inputs)
	if err != nil {
		return Plan{}, err
	}
	decided := Plan{
		Trigger:         inputs.Trigger,
		Mode:            FullMode,
		Ref:             inputs.Ref,
		Commit:          inputs.Commit,
		BenchmarkCommit: inputs.BenchmarkCommit,
		Contenders:      decidedContenders,
		Workloads:       []Workload{Throughput, FixedRateLatency},
		Schedule:        fullSchedule,
		Resources:       fourCoreRunnerSplit,
		Network:         hostNetwork,
		Publish:         publish,
	}
	addBaselineRelease(&decided, inputs)
	applySmokeMode(&decided, inputs)
	return decided, nil
}

const shortCommitLength = 7

func (p *Plan) Skipped() bool { return p.SkipReason != "" }

func (p *Plan) ShortCommit() string { return shortCommit(p.Commit) }

func shortCommit(commit string) string {
	if len(commit) > shortCommitLength {
		return commit[:shortCommitLength]
	}
	return commit
}
