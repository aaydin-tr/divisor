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
	wrr := New(nil, backends)
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
	wrr := New(nil, backends)
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
