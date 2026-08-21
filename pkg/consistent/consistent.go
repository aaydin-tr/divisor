package consistent

import (
	"sort"
	"strconv"
	"sync/atomic"

	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/helper"
)

type hashRing []uint32

func (h hashRing) Len() int {
	return len(h)
}

func (h hashRing) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h hashRing) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

type Node struct {
	Proxy proxy.IProxyClient
	Addr  string
	Id    int
}

type ringSnapshot struct {
	nodes   map[uint32]*Node
	numbers hashRing
}

type ConsistentHash struct {
	ring        atomic.Pointer[ringSnapshot]
	hashFunc    func([]byte) uint32
	virtualRepl int
}

func NewConsistentHash(replicas int, hashFunc func([]byte) uint32) *ConsistentHash {
	c := &ConsistentHash{
		virtualRepl: replicas,
		hashFunc:    hashFunc,
	}
	c.ring.Store(&ringSnapshot{nodes: make(map[uint32]*Node)})
	return c
}

func (c *ConsistentHash) AddNode(node *Node) {
	old := c.ring.Load()
	newNodes := make(map[uint32]*Node, len(old.nodes)+c.virtualRepl)
	for hash, n := range old.nodes {
		newNodes[hash] = n
	}
	newNumbers := make(hashRing, 0, len(old.numbers)+c.virtualRepl)
	newNumbers = append(newNumbers, old.numbers...)

	for i := 0; i < c.virtualRepl; i++ {
		hash := c.hashFunc(virtualNodeKey(node, i))
		newNodes[hash] = node
		newNumbers = append(newNumbers, hash)
	}
	sort.Sort(newNumbers)
	c.ring.Store(&ringSnapshot{nodes: newNodes, numbers: newNumbers})
}

func (c *ConsistentHash) RemoveNode(node *Node) {
	old := c.ring.Load()
	removed := make(map[uint32]bool, c.virtualRepl)
	for i := 0; i < c.virtualRepl; i++ {
		removed[c.hashFunc(virtualNodeKey(node, i))] = true
	}

	newNodes := make(map[uint32]*Node, len(old.nodes))
	for hash, n := range old.nodes {
		if !removed[hash] {
			newNodes[hash] = n
		}
	}
	newNumbers := make(hashRing, 0, len(old.numbers))
	for _, hash := range old.numbers {
		if !removed[hash] {
			newNumbers = append(newNumbers, hash)
		}
	}
	c.ring.Store(&ringSnapshot{nodes: newNodes, numbers: newNumbers})
}

// Keyed by Id first: two Nodes may share an Addr (the same Backend address
// listed twice) and must still own disjoint virtual nodes.
func virtualNodeKey(node *Node, i int) []byte {
	return helper.S2B(strconv.Itoa(node.Id) + "|" + strconv.Itoa(i) + "|" + node.Addr)
}

func (c *ConsistentHash) GetNode(hash uint32) *Node {
	ring := c.ring.Load()
	if ring.numbers.Len() == 0 {
		return nil
	}

	i := sort.Search(ring.numbers.Len(), func(i int) bool { return ring.numbers[i] >= hash })
	if i == ring.numbers.Len() {
		i = 0
	}

	return ring.nodes[ring.numbers[i]]
}
