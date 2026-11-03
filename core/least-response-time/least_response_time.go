package least_response_time

import (
	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// A Score changes only when least-response-time picks its Backend, so one
// failure penalty would keep a healthy Backend out until it goes Down.
const probeRoundsBeforeTrialRequest = 3

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

func (l *leastResponseTime) Pick(*fasthttp.RequestCtx) *pool.Backend {
	aliveBackends := l.AliveBackends()
	if len(aliveBackends) == 0 {
		return nil
	}
	picked := aliveBackends[0]
	leastResTime := picked.Proxy.RecentResponseTime()
	for _, candidate := range aliveBackends {
		resTime := candidate.Proxy.RecentResponseTime()
		// 0 means unmeasured (never answered, or just Rejoined), and an expired
		// Score is due its Trial request: either wins to get a fresh sample.
		if resTime == 0 || candidate.Proxy.ClaimTrialRequest() {
			return candidate
		}
		if resTime < leastResTime {
			picked = candidate
			leastResTime = resTime
		}
	}
	return picked
}
