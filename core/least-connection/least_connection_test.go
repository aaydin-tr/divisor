package least_connection

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

func TestLeastConnectionPicksFewestPending(t *testing.T) {
	t.Run("picks the Backend with the fewest Pending requests, not the first improvement", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		mocks.MockProxyOf(backends[0]).Pending = 5
		mocks.MockProxyOf(backends[1]).Pending = 3
		mocks.MockProxyOf(backends[2]).Pending = 1
		lc := New(nil, backends)
		mocks.JoinAll(lc, backends)

		for i := 0; i < 10; i++ {
			assert.Same(t, backends[2], lc.Pick(nil), "call %d", i)
		}
	})

	t.Run("rotates among equally loaded Backends", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		lc := New(nil, backends)
		mocks.JoinAll(lc, backends)

		seen := map[*pool.Backend]int{}
		for i := 0; i < 9; i++ {
			seen[lc.Pick(nil)]++
		}
		assert.Equal(t, map[*pool.Backend]int{backends[0]: 3, backends[1]: 3, backends[2]: 3}, seen)
	})

	t.Run("a Backend that Rejoins mid-rotation takes its turn", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		for _, b := range backends {
			mocks.MockProxyOf(b).Pending = 2
		}
		lc := New(nil, backends)
		mocks.JoinAll(lc, backends[:2])
		lc.Pick(nil)

		lc.Join(backends[2])
		reached := false
		for i := 0; i < len(backends) && !reached; i++ {
			reached = lc.Pick(nil) == backends[2]
		}
		assert.True(t, reached, "an equally loaded Rejoined Backend must be picked within one rotation")
	})

	t.Run("the rotation shrinking never indexes past it", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		lc := New(nil, backends)
		mocks.JoinAll(lc, backends)
		lc.Pick(nil)
		lc.Pick(nil)

		lc.Leave(backends[1])
		lc.Leave(backends[2])
		for i := 0; i < 5; i++ {
			assert.Same(t, backends[0], lc.Pick(nil))
		}
		lc.Leave(backends[0])
		assert.Nil(t, lc.Pick(nil))
	})
}
