package least_response_time

import (
	"math/rand/v2"
	"slices"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// A Score changes only when least-response-time picks its Backend, so one
// failure penalty would keep a healthy Backend out until it goes Down.
const probeRoundsBeforeTrialRequest = 3

// A Score of 0: the Backend never answered, or just Rejoined.
const unmeasured = 0

// Any positive value: a Score of 0 would erase the Pending requests term.
const scoreWhenNothingIsMeasured = 1

type leastResponseTime struct {
	pool.Rotation
	// Only the Pool's one writer touches it, through Join, Leave and
	// ProbeRoundStarted; Pick never does.
	probeRoundTallyByBackend map[*pool.Backend]*probeRoundTally
}

type probeRoundTally struct {
	backend                      *pool.Backend
	requestCountAtLastProbeRound uint64
	probeRoundsBypassed          int
}

// bypassedSinceLastProbeRound reports whether no request was routed to the
// Backend since the last round, and records the count for the next one.
func (t *probeRoundTally) bypassedSinceLastProbeRound() bool {
	requestCount := t.backend.Proxy.TotalRequestCount()
	if requestCount == t.requestCountAtLastProbeRound {
		return true
	}
	t.requestCountAtLastProbeRound = requestCount
	t.probeRoundsBypassed = 0
	return false
}

// countBypassedRound reports whether this round is the one that makes the
// Backend's Score due a Trial request.
func (t *probeRoundTally) countBypassedRound() (trialRequestDue bool) {
	t.probeRoundsBypassed++
	if t.probeRoundsBypassed < probeRoundsBeforeTrialRequest {
		return false
	}
	t.probeRoundsBypassed = 0
	return true
}

func New(*config.Config) pool.Balancer {
	return &leastResponseTime{probeRoundTallyByBackend: map[*pool.Backend]*probeRoundTally{}}
}

// Join starts a Backend unmeasured: after a Rejoin, its Score from before it
// went Down, possibly a failure penalty, says nothing about it now.
func (l *leastResponseTime) Join(backend *pool.Backend) {
	backend.Proxy.ResetRecentResponseTime()
	l.probeRoundTallyByBackend[backend] = &probeRoundTally{backend: backend, requestCountAtLastProbeRound: backend.Proxy.TotalRequestCount()}
	l.Rotation.Join(backend)
}

func (l *leastResponseTime) Leave(backend *pool.Backend) {
	l.Rotation.Leave(backend)
	delete(l.probeRoundTallyByBackend, backend)
}

// ProbeRoundStarted expires the Score of every Alive Backend that traffic
// bypassed for probeRoundsBeforeTrialRequest rounds: requests went to other
// Backends, none to it. A round no request reached bypassed none of them.
func (l *leastResponseTime) ProbeRoundStarted() {
	var bypassedTallies []*probeRoundTally
	for _, tally := range l.probeRoundTallyByBackend {
		if tally.bypassedSinceLastProbeRound() {
			bypassedTallies = append(bypassedTallies, tally)
		}
	}
	if len(bypassedTallies) == len(l.probeRoundTallyByBackend) {
		return
	}
	for _, tally := range bypassedTallies {
		if tally.countBypassedRound() {
			tally.backend.Proxy.ExpireRecentResponseTime()
		}
	}
}

// Pick compares two different Alive Backends drawn at random by Score ×
// (Pending requests + 1). The worst never wins, so a Trial request comes first.
func (l *leastResponseTime) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := l.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	for _, candidate := range aliveBackends {
		if candidate.Proxy.ClaimTrialRequest() {
			return candidate
		}
	}
	if len(aliveBackends) == 1 {
		return aliveBackends[0]
	}
	first, second := drawTwoDifferentBackends(aliveBackends)
	if loadWeightedScore(second, aliveBackends) < loadWeightedScore(first, aliveBackends) {
		return second
	}
	return first
}

func drawTwoDifferentBackends(aliveBackends []*pool.Backend) (first, second *pool.Backend) {
	firstIndex := rand.IntN(len(aliveBackends))      //nolint:gosec
	secondIndex := rand.IntN(len(aliveBackends) - 1) //nolint:gosec
	if secondIndex >= firstIndex {
		secondIndex++
	}
	return aliveBackends[firstIndex], aliveBackends[secondIndex]
}

func loadWeightedScore(backend *pool.Backend, aliveBackends []*pool.Backend) float64 {
	score := backend.Proxy.RecentResponseTime()
	if score == unmeasured {
		score = medianMeasuredScore(aliveBackends)
	}
	return score * float64(backend.Proxy.PendingRequests()+1)
}

// With nothing measured, every Backend competes alike and Pending requests decide.
func medianMeasuredScore(aliveBackends []*pool.Backend) float64 {
	measuredScores := make([]float64, 0, len(aliveBackends))
	for _, backend := range aliveBackends {
		if score := backend.Proxy.RecentResponseTime(); score != unmeasured {
			measuredScores = append(measuredScores, score)
		}
	}
	if len(measuredScores) == 0 {
		return scoreWhenNothingIsMeasured
	}
	slices.Sort(measuredScores)
	middle := len(measuredScores) / 2
	if len(measuredScores)%2 == 1 {
		return measuredScores[middle]
	}
	return (measuredScores[middle-1] + measuredScores[middle]) / 2
}
