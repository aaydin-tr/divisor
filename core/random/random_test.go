package random

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

func TestRandomPicksOnlyAliveBackends(t *testing.T) {
	backends := mocks.NewBackends(3)
	r := New(nil, backends)
	assert.Nil(t, r.Pick(nil), "nothing Alive → nil")

	for _, b := range backends {
		r.Join(b)
	}
	r.Leave(backends[2])

	seen := map[*pool.Backend]int{}
	for i := 0; i < 200; i++ {
		seen[r.Pick(nil)]++
	}
	assert.NotContains(t, seen, backends[2], "a Backend that Left is never picked")
	assert.Positive(t, seen[backends[0]])
	assert.Positive(t, seen[backends[1]])
}
