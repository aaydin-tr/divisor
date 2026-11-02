package consistent

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/aaydin-tr/divisor/pkg/helper"
)

// placement is one node's claim on a position: two keys can hash a virtual
// node to the same one.
type placement[Owner comparable] struct {
	ringKey string
	owner   Owner
}

// ringSnapshot is never mutated once stored, so OwnerOf reads it unlocked.
type ringSnapshot[Owner comparable] struct {
	// placementsAt holds each position's claims sorted by key; the first owns it.
	placementsAt map[uint32][]placement[Owner]
	positions    []uint32
}

// ConsistentHash places virtual nodes by their ring key alone: rings holding
// the same keys agree on every hash. AddNode and RemoveNode need one writer.
type ConsistentHash[Owner comparable] struct {
	ring                atomic.Pointer[ringSnapshot[Owner]]
	hashFunc            func([]byte) uint32
	virtualNodesPerNode int
}

func NewConsistentHash[Owner comparable](virtualNodesPerNode int, hashFunc func([]byte) uint32) *ConsistentHash[Owner] {
	c := &ConsistentHash[Owner]{
		virtualNodesPerNode: virtualNodesPerNode,
		hashFunc:            hashFunc,
	}
	c.ring.Store(&ringSnapshot[Owner]{placementsAt: make(map[uint32][]placement[Owner])})
	return c
}

// AddNode gives owner the virtual nodes of ringKey, taking them over from any
// owner placed under the same key before.
func (c *ConsistentHash[Owner]) AddNode(ringKey string, owner Owner) {
	old := c.ring.Load()
	newPlacementsAt := maps.Clone(old.placementsAt)
	newPositions := slices.Grow(slices.Clone(old.positions), c.virtualNodesPerNode)
	for i := 0; i < c.virtualNodesPerNode; i++ {
		position := c.hashFunc(virtualNodeKey(ringKey, i))
		if len(newPlacementsAt[position]) == 0 {
			newPositions = append(newPositions, position)
		}
		newPlacementsAt[position] = withPlacement(newPlacementsAt[position], placement[Owner]{ringKey: ringKey, owner: owner})
	}
	slices.Sort(newPositions)
	c.ring.Store(&ringSnapshot[Owner]{placementsAt: newPlacementsAt, positions: newPositions})
}

// withPlacement returns a new slice: the previous snapshot still reads the old one.
func withPlacement[Owner comparable](placements []placement[Owner], added placement[Owner]) []placement[Owner] {
	updated := make([]placement[Owner], 0, len(placements)+1)
	for _, placed := range placements {
		if placed.ringKey != added.ringKey {
			updated = append(updated, placed)
		}
	}
	updated = append(updated, added)
	slices.SortFunc(updated, func(left, right placement[Owner]) int { return strings.Compare(left.ringKey, right.ringKey) })
	return updated
}

// RemoveNode drops the virtual nodes of ringKey that owner still holds.
func (c *ConsistentHash[Owner]) RemoveNode(ringKey string, owner Owner) {
	old := c.ring.Load()
	newPlacementsAt := maps.Clone(old.placementsAt)
	removed := placement[Owner]{ringKey: ringKey, owner: owner}
	for i := 0; i < c.virtualNodesPerNode; i++ {
		position := c.hashFunc(virtualNodeKey(ringKey, i))
		remaining := slices.DeleteFunc(slices.Clone(newPlacementsAt[position]), func(placed placement[Owner]) bool { return placed == removed })
		if len(remaining) == 0 {
			delete(newPlacementsAt, position)
			continue
		}
		newPlacementsAt[position] = remaining
	}

	newPositions := make([]uint32, 0, len(newPlacementsAt))
	for _, position := range old.positions {
		if _, placed := newPlacementsAt[position]; placed {
			newPositions = append(newPositions, position)
		}
	}
	c.ring.Store(&ringSnapshot[Owner]{placementsAt: newPlacementsAt, positions: newPositions})
}

func virtualNodeKey(ringKey string, i int) []byte {
	return helper.S2B(ringKey + "|" + strconv.Itoa(i))
}

// OwnerOf returns the owner of the first position at or after hash, wrapping
// past the last; false when the ring is empty.
func (c *ConsistentHash[Owner]) OwnerOf(hash uint32) (Owner, bool) {
	ring := c.ring.Load()
	if len(ring.positions) == 0 {
		var noOwner Owner
		return noOwner, false
	}

	i, _ := slices.BinarySearch(ring.positions, hash)
	if i == len(ring.positions) {
		i = 0
	}
	return ring.placementsAt[ring.positions[i]][0].owner, true
}
