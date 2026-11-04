package plan

// SmokeTrigger is a PR check: a short run that proves every Contender is in
// the Equivalent setup, never numbers anyone reads.
const SmokeTrigger Trigger = "smoke"

const SmokeMode Mode = "smoke"

// EquivalentSetupCheck sends Requests through each Contender over a few
// keep-alive Connections and checks what the Benchmark backends saw.
type EquivalentSetupCheck struct {
	Requests    int `json:"requests"`
	Connections int `json:"connections"`
}

var smokeSchedule = Schedule{
	Rounds:                  1,
	WarmUpSeconds:           0,
	MeasurementSeconds:      3,
	ReadinessTimeoutSeconds: 30,
	EquivalentSetupCheck:    &EquivalentSetupCheck{Requests: 2000, Connections: 8},
}

func withSmokeMode(decided *Plan, inputs *Inputs) {
	if inputs.Trigger != SmokeTrigger {
		return
	}
	decided.Mode = SmokeMode
	decided.Schedule = smokeSchedule
}
