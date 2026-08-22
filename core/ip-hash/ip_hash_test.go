package ip_hash

import (
	"fmt"
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/stretchr/testify/assert"
)

func ipHashConfig(n int) *config.Config {
	cfg := &config.Config{HashFunc: helper.HashFunc}
	for i := 0; i < n; i++ {
		cfg.Backends = append(cfg.Backends, config.Backend{Url: fmt.Sprintf("localhost:%d", 8080+i)})
	}
	return cfg
}

const clientSamples = 256

func clientIP(i int) string { return fmt.Sprintf("10.%d.%d.%d", i%7, i/3, i) }

func TestIPHashIsStablePerClient(t *testing.T) {
	backends := mocks.NewBackends(3)
	ih := New(ipHashConfig(3), backends)
	mocks.JoinAll(ih, backends)

	seen := map[*pool.Backend]int{}
	for i := 0; i < clientSamples; i++ {
		first := ih.Pick(mocks.RequestFrom(clientIP(i)))
		assert.Same(t, first, ih.Pick(mocks.RequestFrom(clientIP(i))), "client %d must map to one Backend", i)
		seen[first]++
	}
	assert.Len(t, seen, 3, "clients spread over every Alive Backend")
}

func TestIPHashAcceptsBackendsInAnyOrder(t *testing.T) {
	backends := mocks.NewBackends(3)
	shuffled := []*pool.Backend{backends[2], backends[0], backends[1]}
	ih := New(ipHashConfig(3), shuffled)
	mocks.JoinAll(ih, shuffled)

	for i := 0; i < clientSamples; i++ {
		picked := ih.Pick(mocks.RequestFrom(clientIP(i)))
		assert.Same(t, backends[picked.Index], picked, "client %d resolved to a Backend whose Index does not match", i)
	}
}

func TestIPHashNothingAlive(t *testing.T) {
	backends := mocks.NewBackends(2)
	ih := New(ipHashConfig(2), backends)
	assert.Nil(t, ih.Pick(mocks.RequestFrom("10.0.0.1")))

	ih.Join(backends[0])
	ih.Leave(backends[0])
	assert.Nil(t, ih.Pick(mocks.RequestFrom("10.0.0.1")))
}

func TestDuplicateAddressTwinKeepsItsVirtualNodes(t *testing.T) {
	backends := mocks.NewBackends(2)
	backends[1].Addr = backends[0].Addr
	cfg := ipHashConfig(2)
	cfg.Backends[1].Url = cfg.Backends[0].Url
	ih := New(cfg, backends)
	mocks.JoinAll(ih, backends)

	before := make([]*pool.Backend, clientSamples)
	for i := range before {
		before[i] = ih.Pick(mocks.RequestFrom(clientIP(i)))
	}

	ih.Leave(backends[0])
	for i := 0; i < clientSamples; i++ {
		assert.Same(t, backends[1], ih.Pick(mocks.RequestFrom(clientIP(i))), "client %d must fail over to the twin", i)
	}

	ih.Join(backends[0])
	for i := 0; i < clientSamples; i++ {
		assert.Same(t, before[i], ih.Pick(mocks.RequestFrom(clientIP(i))), "client %d must route as it did before the flap", i)
	}
}
