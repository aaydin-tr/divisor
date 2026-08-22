package least_response_time

import (
	"testing"

	"github.com/aaydin-tr/divisor/mocks"

	"github.com/stretchr/testify/assert"
)

func TestLeastResponseTimePicksFastest(t *testing.T) {
	t.Run("picks the fastest Backend when all are measured", func(t *testing.T) {
		backends := mocks.NewBackends(3)
		mocks.MockProxyOf(backends[0]).ResTime = 5
		mocks.MockProxyOf(backends[1]).ResTime = 3
		mocks.MockProxyOf(backends[2]).ResTime = 1
		lrt := New(nil, backends)
		mocks.JoinAll(lrt, backends)

		assert.Same(t, backends[2], lrt.Pick(nil))
	})

	t.Run("distinguishes sub-millisecond Backends", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		mocks.MockProxyOf(backends[0]).ResTime = 0.8
		mocks.MockProxyOf(backends[1]).ResTime = 0.2
		lrt := New(nil, backends)
		mocks.JoinAll(lrt, backends)

		assert.Same(t, backends[1], lrt.Pick(nil))
	})

	t.Run("prefers a Backend that has not answered yet", func(t *testing.T) {
		backends := mocks.NewBackends(2)
		mocks.MockProxyOf(backends[0]).ResTime = 1
		lrt := New(nil, backends)
		mocks.JoinAll(lrt, backends)

		assert.Same(t, backends[1], lrt.Pick(nil))
	})

	t.Run("nothing Alive → nil", func(t *testing.T) {
		lrt := New(nil, nil)
		assert.Nil(t, lrt.Pick(nil))
	})
}
