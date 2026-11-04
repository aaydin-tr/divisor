package report

const changeDecimals = 4

// baselineComparisonJSON carries a tag in baseline_release, or null and the
// reason there is none.
type baselineComparisonJSON struct {
	BaselineRelease  *string      `json:"baseline_release"`
	NoBaselineReason string       `json:"no_baseline_reason,omitempty"`
	NoiseFloor       float64      `json:"noise_floor"`
	Changes          []changeJSON `json:"changes"`
}

// changeJSON's change and noise_threshold are fractions, null when not compared.
type changeJSON struct {
	Workload          string   `json:"workload"`
	Number            string   `json:"number"`
	Change            *float64 `json:"change"`
	NoiseThreshold    *float64 `json:"noise_threshold"`
	Verdict           Verdict  `json:"verdict"`
	NotComparedReason string   `json:"not_compared_reason,omitempty"`
}

func baselineComparisonAsJSON(comparison BaselineComparison) baselineComparisonJSON {
	document := baselineComparisonJSON{
		NoBaselineReason: comparison.BaselineRelease.NoBaselineReason,
		NoiseFloor:       NoiseFloor,
		Changes:          []changeJSON{},
	}
	if comparison.BaselineRelease.Exists() {
		document.BaselineRelease = &comparison.BaselineRelease.Tag
	}
	for i := range comparison.Changes {
		change := &comparison.Changes[i]
		changeDocument := changeJSON{
			Workload:          change.Workload.ID,
			Number:            change.Number.JSONName,
			Verdict:           change.Verdict,
			NotComparedReason: change.NotComparedReason,
		}
		if change.Verdict != NotCompared {
			fraction, noiseThreshold := roundedTo(change.Fraction, changeDecimals), roundedTo(change.NoiseThreshold, changeDecimals)
			changeDocument.Change, changeDocument.NoiseThreshold = &fraction, &noiseThreshold
		}
		document.Changes = append(document.Changes, changeDocument)
	}
	return document
}
