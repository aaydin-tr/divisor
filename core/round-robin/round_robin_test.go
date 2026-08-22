package round_robin

import (
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

func TestRoundRobinTakesTurns(t *testing.T) {
	backends := mocks.NewBackends(3)
	rr := New(nil, backends)
	for _, b := range backends {
		rr.Join(b)
	}

	var picks []*pool.Backend
	for i := 0; i < 6; i++ {
		picks = append(picks, rr.Pick(nil))
	}
	assert.Equal(t, []*pool.Backend{backends[1], backends[2], backends[0], backends[1], backends[2], backends[0]}, picks)
}

func TestRoundRobinSkipsBackendsThatLeft(t *testing.T) {
	backends := mocks.NewBackends(3)
	rr := New(nil, backends)
	for _, b := range backends {
		rr.Join(b)
	}
	rr.Leave(backends[1])

	for i := 0; i < 6; i++ {
		assert.NotSame(t, backends[1], rr.Pick(nil))
	}

	rr.Leave(backends[0])
	rr.Leave(backends[2])
	assert.Nil(t, rr.Pick(nil), "nothing Alive → nil")

	rr.Join(backends[1])
	assert.Same(t, backends[1], rr.Pick(nil))
}
