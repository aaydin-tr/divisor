package report

import (
	"fmt"
	"strings"
)

const percentPerFraction = 100

var verdictWording = map[Verdict]string{
	Improvement: "an improvement",
	Regression:  "a regression",
}

// leadLine is what changed against the Baseline release, the section's first line.
func (r *Report) leadLine() string {
	comparison := r.BaselineComparison
	if !comparison.BaselineRelease.Exists() {
		return "**No Baseline release:** " + comparison.BaselineRelease.NoBaselineReason + "."
	}
	sentences := []string{fmt.Sprintf("**Against the Baseline release %s:**", comparison.BaselineRelease.Tag)}
	for i := range comparison.Changes {
		sentences = append(sentences, changeSentence(&comparison.Changes[i]))
	}
	return strings.Join(sentences, " ")
}

func changeSentence(change *Change) string {
	measured := change.Workload.Name + " " + change.Number.Name
	switch change.Verdict {
	case NotCompared:
		return fmt.Sprintf("%s not compared: %s.", measured, change.NotComparedReason)
	case WithinNoise:
		return fmt.Sprintf("%s %s, within noise (±%s).", measured, signedPercent(change.Fraction), percent(change.NoiseThreshold))
	}
	return fmt.Sprintf("%s %s, %s.", measured, signedPercent(change.Fraction), verdictWording[change.Verdict])
}

func signedPercent(fraction float64) string {
	return fmt.Sprintf("%+.1f%%", fraction*percentPerFraction)
}

func percent(fraction float64) string {
	return fmt.Sprintf("%.1f%%", fraction*percentPerFraction)
}
