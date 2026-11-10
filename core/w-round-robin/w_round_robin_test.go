package w_round_robin

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

func TestWeightedRoundRobinSharesTrafficByWeight(t *testing.T) {
	backends := mocks.NewBackends(2)
	backends[0].Weight = 3
	backends[1].Weight = 1
	wrr := New(nil)
	for _, b := range backends {
		wrr.Join(b)
	}

	seen := map[*pool.Backend]int{}
	for i := 0; i < 40; i++ {
		seen[wrr.Pick(nil)]++
	}
	assert.Equal(t, map[*pool.Backend]int{backends[0]: 30, backends[1]: 10}, seen)
}

func TestWeightedRoundRobinLeaveRemovesEveryShare(t *testing.T) {
	backends := mocks.NewBackends(2)
	backends[0].Weight = 3
	backends[1].Weight = 2
	wrr := New(nil)
	for _, b := range backends {
		wrr.Join(b)
	}

	wrr.Leave(backends[0])
	for i := 0; i < 10; i++ {
		assert.Same(t, backends[1], wrr.Pick(nil))
	}

	wrr.Leave(backends[1])
	assert.Nil(t, wrr.Pick(nil))

	wrr.Join(backends[0])
	for i := 0; i < 10; i++ {
		assert.Same(t, backends[0], wrr.Pick(nil))
	}
}

// With weights reduced by their greatest common divisor, every run of
// sum(weight/gcd) consecutive Picks holds each Backend's exact share.
func assertEveryRunOfPicksShares(t *testing.T, wrr pool.Balancer, want map[*pool.Backend]int) {
	t.Helper()
	runLength := 0
	for _, share := range want {
		runLength += share
	}
	for run := 0; run < 20; run++ {
		seen := map[*pool.Backend]int{}
		for range runLength {
			seen[wrr.Pick(nil)]++
		}
		assert.Equal(t, want, seen, "run %d of %d Picks", run, runLength)
	}
}

func TestWeightedRoundRobinReducesWeightsByTheirGreatestCommonDivisor(t *testing.T) {
	backends := mocks.NewBackends(2)
	backends[0].Weight = 100
	backends[1].Weight = 200
	wrr := New(nil)
	for _, b := range backends {
		wrr.Join(b)
	}

	assertEveryRunOfPicksShares(t, wrr, map[*pool.Backend]int{backends[0]: 1, backends[1]: 2})
}

func TestWeightedRoundRobinKeepsSharesRightWhenTheGreatestCommonDivisorChanges(t *testing.T) {
	backends := mocks.NewBackends(3)
	backends[0].Weight = 200
	backends[1].Weight = 400
	backends[2].Weight = 100
	wrr := New(nil)
	wrr.Join(backends[0])
	wrr.Join(backends[1])
	assertEveryRunOfPicksShares(t, wrr, map[*pool.Backend]int{backends[0]: 1, backends[1]: 2})

	wrr.Join(backends[2])
	assertEveryRunOfPicksShares(t, wrr, map[*pool.Backend]int{backends[0]: 2, backends[1]: 4, backends[2]: 1})

	wrr.Leave(backends[2])
	assertEveryRunOfPicksShares(t, wrr, map[*pool.Backend]int{backends[0]: 1, backends[1]: 2})
}
