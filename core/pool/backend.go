package pool

import (
	"sync/atomic"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/internal/proxy"
)

// Backend is one `backends` config entry; its identity is Index (the config
// position at boot, a never-used one when a Reload adds it), so the same
// address listed twice is two Backends.
type Backend struct {
	Proxy               proxy.IProxyClient
	Addr                string
	ProbeURL            string
	ExpectedProbeStatus types.ExpectedProbeStatus
	Index               int
	// AddressOccurrence counts the earlier entries sharing its address in the
	// current config (RingBalancer placement); only the Pool's writer touches it.
	AddressOccurrence int
	Weight            uint
	isAlive           atomic.Bool

	// Touched only by Probe rounds, under the Pool's transitionsMu.
	requestCountAtLastProbeRound uint64
	probeRoundsSinceLastPick     int
}

func (b *Backend) IsAlive() bool {
	return b.isAlive.Load()
}

// A score changes only when least-response-time picks its Backend, so one
// failure penalty would keep a healthy Backend out until it goes Down.
const probeRoundsBeforeTrialRequest = 3

// expireScoreIfNotPicked judges the interval since the last Probe round, so a
// round calls it before its Probe can change the Backend's liveness.
func (b *Backend) expireScoreIfNotPicked() {
	requestCount := b.Proxy.Stat().TotalReqCount
	if !b.isAlive.Load() || requestCount != b.requestCountAtLastProbeRound {
		b.requestCountAtLastProbeRound = requestCount
		b.probeRoundsSinceLastPick = 0
		return
	}
	b.probeRoundsSinceLastPick++
	if b.probeRoundsSinceLastPick == probeRoundsBeforeTrialRequest {
		b.probeRoundsSinceLastPick = 0
		b.Proxy.ExpireRecentResponseTime()
	}
}
