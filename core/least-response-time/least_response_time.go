package least_response_time

import (
	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

// A score changes only when least-response-time picks its Backend, so one
// failure penalty would keep a healthy Backend out until it goes Down.
const probeRoundsBeforeTrialRequest = 3

type leastResponseTime struct {
	pool.Rotation
	// Only the Pool's one writer touches it, through Join, Leave and
	// ProbeRoundStarted; Pick never does.
	probeRoundTallyByBackend map[*pool.Backend]*probeRoundTally
}

type probeRoundTally struct {
	requestCountAtLastProbeRound uint64
	probeRoundsSinceLastRequest  int
}

func New(*config.Config) pool.Balancer {
	return &leastResponseTime{probeRoundTallyByBackend: map[*pool.Backend]*probeRoundTally{}}
}

// Join starts a Backend unmeasured: after a Rejoin, its score from before it
// went Down, possibly a failure penalty, says nothing about it now.
func (l *leastResponseTime) Join(backend *pool.Backend) {
	backend.Proxy.ResetRecentResponseTime()
	l.probeRoundTallyByBackend[backend] = &probeRoundTally{requestCountAtLastProbeRound: backend.Proxy.Stat().TotalReqCount}
	l.Rotation.Join(backend)
}

func (l *leastResponseTime) Leave(backend *pool.Backend) {
	l.Rotation.Leave(backend)
	delete(l.probeRoundTallyByBackend, backend)
}

// ProbeRoundStarted expires the score of every Alive Backend no request was
// routed to for probeRoundsBeforeTrialRequest rounds.
func (l *leastResponseTime) ProbeRoundStarted() {
	for backend, tally := range l.probeRoundTallyByBackend {
		requestCount := backend.Proxy.Stat().TotalReqCount
		if requestCount != tally.requestCountAtLastProbeRound {
			tally.requestCountAtLastProbeRound = requestCount
			tally.probeRoundsSinceLastRequest = 0
			continue
		}
		tally.probeRoundsSinceLastRequest++
		if tally.probeRoundsSinceLastRequest == probeRoundsBeforeTrialRequest {
			tally.probeRoundsSinceLastRequest = 0
			backend.Proxy.ExpireRecentResponseTime()
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
		// score is due its Trial request: either wins to get a fresh sample.
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
