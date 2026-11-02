package ip_hash

import (
	"fmt"
	"testing"

	"github.com/aaydin-tr/divisor/core/pool"
	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
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
	ih := New(ipHashConfig(3))
	mocks.JoinAll(ih, backends)

	seen := map[*pool.Backend]int{}
	for i := 0; i < clientSamples; i++ {
		first := ih.Pick(mocks.RequestFrom(clientIP(i)))
		assert.Same(t, first, ih.Pick(mocks.RequestFrom(clientIP(i))), "client %d must map to one Backend", i)
		seen[first]++
	}
	assert.Len(t, seen, 3, "clients spread over every Alive Backend")
}

func picksOf(balancer pool.Balancer) []*pool.Backend {
	picks := make([]*pool.Backend, clientSamples)
	for i := range picks {
		picks[i] = balancer.Pick(mocks.RequestFrom(clientIP(i)))
	}
	return picks
}

func TestIPHashRoutingDoesNotDependOnJoinOrder(t *testing.T) {
	backends := mocks.NewBackends(3)
	inConfigOrder := New(ipHashConfig(3))
	mocks.JoinAll(inConfigOrder, backends)
	shuffled := New(ipHashConfig(3))
	mocks.JoinAll(shuffled, []*pool.Backend{backends[2], backends[0], backends[1]})

	assert.Equal(t, picksOf(inConfigOrder), picksOf(shuffled))
}

// A Reload that changes an entry (its health_check_path, say) Joins a new
// Backend at the same address before the old one Leaves.
func TestIPHashChangedEntryKeepsItsClients(t *testing.T) {
	backends := mocks.NewBackends(3)
	ih := New(ipHashConfig(3))
	mocks.JoinAll(ih, backends)
	before := picksOf(ih)

	changed := mocks.NewBackend(backends[1].Addr)
	ih.Join(changed)
	ih.Leave(backends[1])

	movedToChanged := 0
	for i, picked := range picksOf(ih) {
		if before[i] == backends[1] {
			assert.Same(t, changed, picked, "client %d must follow its Backend's address to the changed entry", i)
			movedToChanged++
			continue
		}
		assert.Same(t, before[i], picked, "client %d of an untouched Backend moved", i)
	}
	assert.NotZero(t, movedToChanged)
}

func TestIPHashNothingAlive(t *testing.T) {
	backends := mocks.NewBackends(2)
	ih := New(ipHashConfig(2))
	assert.Nil(t, ih.Pick(mocks.RequestFrom("10.0.0.1")))

	ih.Join(backends[0])
	ih.Leave(backends[0])
	assert.Nil(t, ih.Pick(mocks.RequestFrom("10.0.0.1")))
}

func TestDuplicateAddressTwinKeepsItsVirtualNodes(t *testing.T) {
	backends := mocks.NewBackends(2)
	backends[1].Addr = backends[0].Addr
	backends[1].AddressOccurrence = 1 // the Pool numbers entries sharing an address
	cfg := ipHashConfig(2)
	cfg.Backends[1].Url = cfg.Backends[0].Url
	ih := New(cfg)
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

func TestIPHashAddressOccurrenceShiftNeverLeavesTheBackendUnpickable(t *testing.T) {
	backend := mocks.NewBackends(1)[0]
	ih := New(ipHashConfig(1))
	ih.Join(backend)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			previousAddressOccurrence := backend.AddressOccurrence
			backend.AddressOccurrence = 1 - previousAddressOccurrence
			ih.(pool.RingBalancer).MoveToNewAddressOccurrences([]pool.AddressOccurrenceShift{{Backend: backend, PreviousAddressOccurrence: previousAddressOccurrence}})
		}
	}()

	ctx := mocks.RequestFrom("10.1.2.3")
	for {
		select {
		case <-done:
			return
		default:
			if ih.Pick(ctx) == nil {
				t.Fatal("the only Alive Backend was off the ring while its AddressOccurrence shifted")
			}
		}
	}
}

// A Reload swapping two entries at one address moves both in one call; the
// Client IPs of that address must not visit another address meanwhile.
func TestIPHashSwappingTwoEntriesAtOneAddressKeepsTheirClientsOnIt(t *testing.T) {
	backends := mocks.NewBackends(3)
	first, second := backends[0], backends[1]
	second.Addr = first.Addr
	second.AddressOccurrence = 1
	ih := New(ipHashConfig(3))
	mocks.JoinAll(ih, backends)
	var clientsOfTheAddress []*fasthttp.RequestCtx
	for i := 0; i < clientSamples; i++ {
		if ctx := mocks.RequestFrom(clientIP(i)); ih.Pick(ctx).Addr == first.Addr {
			clientsOfTheAddress = append(clientsOfTheAddress, ctx)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			previousOfFirst, previousOfSecond := first.AddressOccurrence, second.AddressOccurrence
			first.AddressOccurrence, second.AddressOccurrence = previousOfSecond, previousOfFirst
			ih.(pool.RingBalancer).MoveToNewAddressOccurrences([]pool.AddressOccurrenceShift{
				{Backend: second, PreviousAddressOccurrence: previousOfSecond},
				{Backend: first, PreviousAddressOccurrence: previousOfFirst},
			})
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			for _, ctx := range clientsOfTheAddress {
				if picked := ih.Pick(ctx); picked.Addr != first.Addr {
					t.Fatalf("a Client IP of %s went to %s while its two entries swapped", first.Addr, picked.Addr)
				}
			}
		}
	}
}

// With 100 virtual nodes per Backend the split is close to even; with the old
// len(backends)² count (9 per Backend here) it was lumpy.
func TestIPHashSpreadsClientsEvenly(t *testing.T) {
	const backendCount, clients = 3, 10000
	backends := mocks.NewBackends(backendCount)
	ih := New(ipHashConfig(backendCount))
	mocks.JoinAll(ih, backends)

	share := map[*pool.Backend]int{}
	for i := 0; i < clients; i++ {
		share[ih.Pick(mocks.RequestFrom(fmt.Sprintf("10.%d.%d.%d", (i>>16)&255, (i>>8)&255, i&255)))]++
	}
	ideal := clients / backendCount
	for b, got := range share {
		assert.InDelta(t, ideal, got, float64(ideal)*0.25, "Backend %s share %d is more than 25%% off the ideal %d", b.Addr, got, ideal)
	}
}

// A Reload hands the Balancer Backends it was not constructed with; only the
// clients they take over move.
func TestIPHashBackendAddedAfterConstructionTakesOverSomeClients(t *testing.T) {
	backends := mocks.NewBackends(2)
	ih := New(ipHashConfig(2))
	mocks.JoinAll(ih, backends)
	before := make([]*pool.Backend, clientSamples)
	for i := range before {
		before[i] = ih.Pick(mocks.RequestFrom(clientIP(i)))
	}

	added := mocks.NewBackend("localhost:9000")
	ih.Join(added)

	movedToAdded := 0
	for i := 0; i < clientSamples; i++ {
		picked := ih.Pick(mocks.RequestFrom(clientIP(i)))
		if picked == added {
			movedToAdded++
			continue
		}
		assert.Same(t, before[i], picked, "client %d moved between the Backends that were already there", i)
	}
	assert.NotZero(t, movedToAdded, "the added Backend takes over some clients")

	ih.Leave(added)
	ih.Leave(backends[0])
	for i := 0; i < clientSamples; i++ {
		assert.Same(t, backends[1], ih.Pick(mocks.RequestFrom(clientIP(i))))
	}
}
