package plan

import "time"

type Schedule struct {
	Rounds                  int `json:"rounds"`
	WarmUpSeconds           int `json:"warm_up_seconds"`
	MeasurementSeconds      int `json:"measurement_seconds"`
	ReadinessTimeoutSeconds int `json:"readiness_timeout_seconds"`
	// EquivalentSetupCheck runs after a Contender is ready, before its Workloads.
	EquivalentSetupCheck *EquivalentSetupCheck `json:"equivalent_setup_check,omitempty"`
}

// fullSchedule's three interleaved rounds give the median (ADR 0009).
var fullSchedule = Schedule{
	Rounds:                  3,
	WarmUpSeconds:           5,
	MeasurementSeconds:      30,
	ReadinessTimeoutSeconds: 30,
}

func (s Schedule) WarmUp() time.Duration { return time.Duration(s.WarmUpSeconds) * time.Second }

func (s Schedule) Measurement() time.Duration {
	return time.Duration(s.MeasurementSeconds) * time.Second
}

func (s Schedule) ReadinessTimeout() time.Duration {
	return time.Duration(s.ReadinessTimeoutSeconds) * time.Second
}

// Turn is one Contender's part of a round: it is started, made ready, run
// through every Workload (each warmed up, then measured) and stopped.
type Turn struct {
	Round     int
	Contender Contender
	Workloads []Workload
}

// Turns interleaves the Contenders, in row order, within every round, so a
// noisy minute on a shared runner hits every Contender instead of one.
func (p *Plan) Turns() []Turn {
	turns := make([]Turn, 0, p.Schedule.Rounds*len(p.Contenders))
	for round := 1; round <= p.Schedule.Rounds; round++ {
		for _, contender := range p.Contenders {
			turns = append(turns, Turn{Round: round, Contender: contender, Workloads: p.Workloads})
		}
	}
	return turns
}
