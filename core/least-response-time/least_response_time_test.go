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

// pickWhileEveryRequestStaysPending counts the requests each Backend took when
// each arrives while every earlier one is still Pending.
func pickWhileEveryRequestStaysPending(lrt pool.Balancer, requestCount int) map[*pool.Backend]int {
	requestCountByBackend := map[*pool.Backend]int{}
	for range requestCount {
		picked := lrt.Pick(nil)
		mocks.MockProxyOf(picked).Pending++
		requestCountByBackend[picked]++
	}
	return requestCountByBackend
}

// pickWhileEveryRequestFinishesFirst counts the requests each Backend took when
// each finishes before the next arrives.
func pickWhileEveryRequestFinishesFirst(lrt pool.Balancer, requestCount int) map[*pool.Backend]int {
	requestCountByBackend := map[*pool.Backend]int{}
	for range requestCount {
		requestCountByBackend[lrt.Pick(nil)]++
	}
	return requestCountByBackend
}

func TestLeastResponseTimeSpreadsConcurrentRequestsFromAColdStart(t *testing.T) {
	t.Run("both Backends unmeasured", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		mocks.JoinAll(lrt, backends)

		requestCountByBackend := pickWhileEveryRequestStaysPending(lrt, 100)

		assert.InDelta(t, 50, requestCountByBackend[backends[0]], 10)
		assert.InDelta(t, 50, requestCountByBackend[backends[1]], 10)
	})

	t.Run("one Backend measured, the other not yet", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		joinWithScores(lrt, backends, 50)

		requestCountByBackend := pickWhileEveryRequestStaysPending(lrt, 100)

		assert.InDelta(t, 50, requestCountByBackend[backends[0]], 10)
		assert.InDelta(t, 50, requestCountByBackend[backends[1]], 10)
	})
}

func TestLeastResponseTimeSpreadsABurstByScoreAndPendingRequests(t *testing.T) {
	backends := mocks.NewBackends(2)
	faster, slower := backends[0], backends[1]
	lrt := New(nil)
	joinWithScores(lrt, backends, 10, 20)

	requestCountByBackend := pickWhileEveryRequestStaysPending(lrt, 30)

	assert.InDelta(t, 20, requestCountByBackend[faster], 2, "twice as fast, so twice the Pending requests")
	assert.InDelta(t, 10, requestCountByBackend[slower], 2)
}

func TestLeastResponseTimeComparesTwoDifferentBackends(t *testing.T) {
	t.Run("picks the lower Score when neither has Pending requests", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		joinWithScores(lrt, backends, 5, 1)

		assert.Equal(t, map[*pool.Backend]int{backends[1]: 10}, pickWhileEveryRequestFinishesFirst(lrt, 10))
	})

	t.Run("distinguishes sub-millisecond Backends", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		lrt := New(nil)
		joinWithScores(lrt, backends, 0.8, 0.2)

		assert.Equal(t, map[*pool.Backend]int{backends[1]: 10}, pickWhileEveryRequestFinishesFirst(lrt, 10))
	})

	t.Run("a lower Score loses to its Pending requests", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		busyFaster, idleSlower := backends[0], backends[1]
		lrt := New(nil)
		joinWithScores(lrt, backends, 1, 3)
		mocks.MockProxyOf(busyFaster).Pending = 3

		assert.Equal(t, map[*pool.Backend]int{idleSlower: 10}, pickWhileEveryRequestFinishesFirst(lrt, 10), "1ms × 4 is worse than 3ms × 1")
	})

	t.Run("the worst Backend never wins, every other one takes requests", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		slowest, slower, fastest := backends[0], backends[1], backends[2]
		lrt := New(nil)
		joinWithScores(lrt, backends, 5, 3, 1)

		requestCountByBackend := pickWhileEveryRequestFinishesFirst(lrt, 300)

		assert.Zero(t, requestCountByBackend[slowest], "drawn with another Backend every time, it loses every comparison")
		assert.Positive(t, requestCountByBackend[slower], "it wins every draw that pairs it with the slowest")
		assert.Positive(t, requestCountByBackend[fastest])
	})

	t.Run("one Alive Backend takes every request", func(t *testing.T) {
		backends := mocks.NewBackends(1)
		lrt := New(nil)
		joinWithScores(lrt, backends, 10_000)

		assert.Same(t, backends[0], lrt.Pick(nil))
	})

	t.Run("nothing Alive → nil", func(t *testing.T) {
		lrt := New(nil)
		assert.Nil(t, lrt.Pick(nil))
	})
}

func TestLeastResponseTimeScoresAnUnmeasuredBackendAsTheMedian(t *testing.T) {
	t.Run("an unmeasured Backend does not win outright: Pending requests decide against an equal Score", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		measured, unmeasuredBackend := backends[0], backends[1]
		lrt := New(nil)
		joinWithScores(lrt, backends, 5)
		mocks.MockProxyOf(unmeasuredBackend).Pending = 1

		assert.Equal(t, map[*pool.Backend]int{measured: 10}, pickWhileEveryRequestFinishesFirst(lrt, 10))
	})

	t.Run("the median of the measured Scores, not their mean", func(t *testing.T) {
		backends := mocks.NewBackends(4)
		fastest, unmeasuredBackend := backends[0], backends[3]
		lrt := New(nil)
		joinWithScores(lrt, backends, 1, 10, 11)
		mocks.MockProxyOf(fastest).Pending = 17
		mocks.MockProxyOf(unmeasuredBackend).Pending = 1

		requestCountByBackend := pickWhileEveryRequestFinishesFirst(lrt, 100)

		assert.Zero(t, requestCountByBackend[unmeasuredBackend], "median 10 × 2 is the worst of 18, 10 and 11; the mean, 7.3 × 2, would beat 18")
	})
}

func TestLeastResponseTimeSendsAnExpiredScoreOneTrialRequestAheadOfTheComparison(t *testing.T) {
	backends := mocks.NewBackends(2)
	faster, expired := backends[0], backends[1]
	lrt := New(nil)
	joinWithScores(lrt, backends, 1, 10_000)

	expired.Proxy.ExpireRecentResponseTime()
	assert.Same(t, expired, lrt.Pick(nil))
	assert.Same(t, faster, lrt.Pick(nil), "one Trial request; the faster wins again")
}

func TestLeastResponseTimeStartsARejoiningBackendUnmeasured(t *testing.T) {
	backends := mocks.NewBackends(2)
	rejoining, busyFaster := backends[0], backends[1]
	lrt := New(nil)
	joinWithScores(lrt, backends, 10_000, 1)
	mocks.MockProxyOf(busyFaster).Pending = 1
	assert.Same(t, busyFaster, lrt.Pick(nil))

	lrt.Leave(rejoining)
	lrt.Join(rejoining)
	assert.Same(t, rejoining, lrt.Pick(nil),
		"its score from before it went Down says nothing about it now: scored as the median, the faster's own Score, it wins on the faster's Pending request")
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
	requestToTheFastestThenQuietRounds := func() {
		fastest.Proxy.ReverseProxyHandler(nil) //nolint:errcheck
		for range 4 {
			lrt.ProbeRoundStarted()
		}
	}

	for range 10 {
		lrt.ProbeRoundStarted()
	}
	requestToTheFastestThenQuietRounds()
	requestToTheFastestThenQuietRounds()
	assert.Zero(t, pickWhileEveryRequestFinishesFirst(lrt, 100)[slowest], "two rounds carried requests, the quiet ones bypassed nobody: no Trial request yet, and the slowest never wins the comparison")

	requestToTheFastestThenQuietRounds()
	assert.ElementsMatch(t, []*pool.Backend{slowest, slower}, []*pool.Backend{lrt.Pick(nil), lrt.Pick(nil)}, "three rounds carried requests, none to either")
	assert.Zero(t, pickWhileEveryRequestFinishesFirst(lrt, 100)[slowest], "one Trial request each, then the comparison again, which the slowest never wins")
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
