package least_response_time

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

// joinWithScores Joins the Backends, then gives each the score in
// milliseconds: a Join starts every Backend unmeasured.
func joinWithScores(lrt pool.Balancer, backends []*pool.Backend, scores ...float64) {
	mocks.JoinAll(lrt, backends)
	for i, score := range scores {
		mocks.MockProxyOf(backends[i]).ResTime = score
	}
}

// serve forwards a request to the Backend Pick chose, as core's Serve does.
func serve(lrt pool.Balancer) *pool.Backend {
	picked := lrt.Pick(nil)
	picked.Proxy.ReverseProxyHandler(nil) //nolint:errcheck
	return picked
}

func TestLeastResponseTimePicksFastest(t *testing.T) {
	t.Run("picks the fastest Backend when all are measured", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		lrt := New(nil)
		joinWithScores(lrt, backends, 5, 3, 1)

		assert.Same(t, backends[2], lrt.Pick(nil))
	})

	t.Run("distinguishes sub-millisecond Backends", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		joinWithScores(lrt, backends, 0.8, 0.2)

		assert.Same(t, backends[1], lrt.Pick(nil))
	})

	t.Run("prefers a Backend that has not answered yet", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		joinWithScores(lrt, backends, 1)

		assert.Same(t, backends[1], lrt.Pick(nil))
	})

	t.Run("an expired score gets one trial request ahead of a faster Backend", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		faster, expired := backends[0], backends[1]
		lrt := New(nil)
		joinWithScores(lrt, backends, 1, 10_000)

		expired.Proxy.ExpireRecentResponseTime()
		assert.Same(t, expired, lrt.Pick(nil))
		assert.Same(t, faster, lrt.Pick(nil), "one trial request; the fastest wins again")
	})

	t.Run("nothing Alive → nil", func(t *testing.T) {
		lrt := New(nil)
		assert.Nil(t, lrt.Pick(nil))
	})
}

func TestLeastResponseTimeStartsARejoiningBackendUnmeasured(t *testing.T) {
	backends := mocks.NewBackends(2)
	rejoining, faster := backends[0], backends[1]
	lrt := New(nil)
	joinWithScores(lrt, backends, 10_000, 1)
	assert.Same(t, faster, lrt.Pick(nil))

	lrt.Leave(rejoining)
	lrt.Join(rejoining)
	assert.Same(t, rejoining, lrt.Pick(nil), "its score from before it went Down says nothing about it now")
}

func TestLeastResponseTimeSendsATrialRequestAfterThreeProbeRoundsWithoutOne(t *testing.T) {
	backends := mocks.NewBackends(2)
	faster, penalized := backends[0], backends[1]
	lrt := New(nil).(pool.ProbeRoundBalancer)
	joinWithScores(lrt, backends, 1, 10_000)

	for round := 1; round <= 3; round++ {
		assert.Same(t, faster, serve(lrt), "round %d", round)
		lrt.ProbeRoundStarted()
	}

	assert.Same(t, penalized, serve(lrt), "no request reached it for three Probe rounds")
	assert.Same(t, faster, serve(lrt))
}

func TestLeastResponseTimeCountsOnlyProbeRoundsThatCarriedRequests(t *testing.T) {
	backends := mocks.NewBackends(3)
	slowest, slower, fastest := backends[0], backends[1], backends[2]
	lrt := New(nil).(pool.ProbeRoundBalancer)
	joinWithScores(lrt, backends, 5, 3, 1)
	requestThenQuietRounds := func() *pool.Backend {
		picked := serve(lrt)
		for range 4 {
			lrt.ProbeRoundStarted()
		}
		return picked
	}

	for range 10 {
		lrt.ProbeRoundStarted()
	}
	for request := 1; request <= 3; request++ {
		assert.Same(t, fastest, requestThenQuietRounds(), "request %d: a round no request reached bypassed no Backend", request)
	}
	assert.Same(t, slowest, requestThenQuietRounds(), "three rounds carried requests, none to it")
	assert.Same(t, slower, requestThenQuietRounds())
	assert.Same(t, fastest, requestThenQuietRounds(), "one Trial request each, then the fastest again")
}

func TestLeastResponseTimeDoesNotCountProbeRoundsOfABackendThatLeft(t *testing.T) {
	backends := mocks.NewBackends(2)
	down := backends[1]
	lrt := New(nil).(pool.ProbeRoundBalancer)
	joinWithScores(lrt, backends, 1, 10_000)

	lrt.Leave(down)
	for range 3 {
		serve(lrt)
		lrt.ProbeRoundStarted()
	}

	assert.False(t, down.Proxy.ClaimTrialRequest(), "a Backend out of the rotation keeps no Probe-round tally")
}
