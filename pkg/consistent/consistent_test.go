package consistent

import (
	"math"
	"strconv"
	"testing"

	"github.com/aaydin-tr/divisor/pkg/helper"
)

const (
	testVirtualNodes = 100
	hashSamples      = 2000
	hashSampleStride = math.MaxUint32 / hashSamples
)

type owner struct{ name string }

func sampledHash(i int) uint32 { return uint32(i * hashSampleStride) }

func ownersOfSampledHashes(ring *ConsistentHash[*owner]) []*owner {
	owners := make([]*owner, hashSamples)
	for i := range owners {
		owners[i], _ = ring.OwnerOf(sampledHash(i))
	}
	return owners
}

func TestEmptyRingHasNoOwner(t *testing.T) {
	ring := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	if got, ok := ring.OwnerOf(42); ok || got != nil {
		t.Fatalf("OwnerOf on an empty ring = %v, %v; want nil, false", got, ok)
	}

	ownerA := &owner{"a"}
	ring.AddNode("a", ownerA)
	ring.RemoveNode("a", ownerA)
	if got, ok := ring.OwnerOf(42); ok || got != nil {
		t.Fatalf("OwnerOf after the last node left = %v, %v; want nil, false", got, ok)
	}
}

func TestOwnerOfIsTheFirstPositionAtOrAfterTheHash(t *testing.T) {
	positions := map[string]uint32{"low|0": 100, "high|0": 200}
	ring := NewConsistentHash[*owner](1, func(key []byte) uint32 { return positions[string(key)] })
	low, high := &owner{"low"}, &owner{"high"}
	ring.AddNode("low", low)
	ring.AddNode("high", high)

	for _, tc := range []struct {
		hash uint32
		want *owner
	}{{0, low}, {100, low}, {101, high}, {200, high}, {201, low}, {math.MaxUint32, low}} {
		if got, _ := ring.OwnerOf(tc.hash); got != tc.want {
			t.Errorf("OwnerOf(%d) = %s, want %s", tc.hash, got.name, tc.want.name)
		}
	}
}

// Two divisor instances sharing a config must agree however each got there.
func TestPositionsDependOnTheKeysAloneNotOnTheirHistory(t *testing.T) {
	ownerA, ownerB, ownerC, ownerD := &owner{"a"}, &owner{"b"}, &owner{"c"}, &owner{"d"}
	reshaped := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	reshaped.AddNode("a", ownerA)
	reshaped.AddNode("b", ownerB)
	reshaped.AddNode("c", ownerC)
	reshaped.AddNode("d", ownerD)
	reshaped.RemoveNode("b", ownerB)

	fresh := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	fresh.AddNode("d", ownerD)
	fresh.AddNode("c", ownerC)
	fresh.AddNode("a", ownerA)

	reshapedOwners, freshOwners := ownersOfSampledHashes(reshaped), ownersOfSampledHashes(fresh)
	for i := range freshOwners {
		if reshapedOwners[i] != freshOwners[i] {
			t.Fatalf("hash %d: %s on the reshaped ring, %s on the fresh one", sampledHash(i), reshapedOwners[i].name, freshOwners[i].name)
		}
	}
}

// A Reload Joins a changed entry's new Backend before the old one, under the
// same key, Leaves; that Leave must not take the new one's positions.
func TestANewOwnerTakesOverItsKeyAndTheOldOneCannotRemoveIt(t *testing.T) {
	ring := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	previous, other := &owner{"previous"}, &owner{"other"}
	ring.AddNode("shared", previous)
	ring.AddNode("other", other)
	before := ownersOfSampledHashes(ring)

	replacement := &owner{"replacement"}
	ring.AddNode("shared", replacement)
	ring.RemoveNode("shared", previous)

	after := ownersOfSampledHashes(ring)
	for i := range before {
		want := before[i]
		if want == previous {
			want = replacement
		}
		if after[i] != want {
			t.Fatalf("hash %d: got %s, want %s", sampledHash(i), after[i].name, want.name)
		}
	}

	ring.RemoveNode("shared", replacement)
	for i, got := range ownersOfSampledHashes(ring) {
		if got != other {
			t.Fatalf("hash %d: got %s after the key's owner left, want other", sampledHash(i), got.name)
		}
	}
}

// A Reload Joins in another order than a boot does, so a position two keys
// hash to must not go to whichever arrived last.
func TestACollidingPositionGoesToTheLowerKeyWhateverTheOrder(t *testing.T) {
	everyKeyCollides := func([]byte) uint32 { return 7 }
	ownerA, ownerB := &owner{"a"}, &owner{"b"}
	addedAFirst := NewConsistentHash[*owner](1, everyKeyCollides)
	addedAFirst.AddNode("a", ownerA)
	addedAFirst.AddNode("b", ownerB)
	addedBFirst := NewConsistentHash[*owner](1, everyKeyCollides)
	addedBFirst.AddNode("b", ownerB)
	addedBFirst.AddNode("a", ownerA)

	for name, ring := range map[string]*ConsistentHash[*owner]{"a first": addedAFirst, "b first": addedBFirst} {
		if got, _ := ring.OwnerOf(0); got != ownerA {
			t.Errorf("%s: OwnerOf = %s, want a", name, got.name)
		}
		ring.RemoveNode("a", ownerA)
		if got, _ := ring.OwnerOf(0); got != ownerB {
			t.Errorf("%s: OwnerOf after a left = %v, want b to keep the position", name, got)
		}
	}
}

func TestRemovingANodeAndAddingItBackRestoresEveryOwner(t *testing.T) {
	ring := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	ownerA, flapping, ownerC := &owner{"a"}, &owner{"b"}, &owner{"c"}
	ring.AddNode("a", ownerA)
	ring.AddNode("b", flapping)
	ring.AddNode("c", ownerC)
	before := ownersOfSampledHashes(ring)

	ring.RemoveNode("b", flapping)
	for i, got := range ownersOfSampledHashes(ring) {
		if got == flapping {
			t.Fatalf("hash %d still routes to a removed node", sampledHash(i))
		}
		if before[i] != flapping && got != before[i] {
			t.Fatalf("hash %d moved from %s to %s though its node stayed", sampledHash(i), before[i].name, got.name)
		}
	}

	ring.AddNode("b", flapping)
	after := ownersOfSampledHashes(ring)
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("hash %d: %s before the flap, %s after", sampledHash(i), before[i].name, after[i].name)
		}
	}
}

func TestOwnerOfConcurrentWithAddRemove(t *testing.T) {
	ring := NewConsistentHash[*owner](9, helper.HashFunc)
	flapping, steady := &owner{"a"}, &owner{"b"}
	ring.AddNode("a", flapping)
	ring.AddNode("b", steady)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			ring.RemoveNode("a", flapping)
			ring.AddNode("a", flapping)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			for i := uint32(0); i < 8; i++ {
				if _, ok := ring.OwnerOf(i * 500000000); !ok {
					t.Fatal("OwnerOf found no owner while the ring was not empty")
				}
			}
		}
	}
}

func BenchmarkOwnerOf(b *testing.B) {
	ring := NewConsistentHash[*owner](testVirtualNodes, helper.HashFunc)
	for i := 0; i < 10; i++ {
		ring.AddNode("localhost:"+strconv.Itoa(8000+i), &owner{strconv.Itoa(i)})
	}

	b.RunParallel(func(pb *testing.PB) {
		h := uint32(0)
		for pb.Next() {
			h += 2654435761
			ring.OwnerOf(h)
		}
	})
}
