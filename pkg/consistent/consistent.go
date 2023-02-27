package consistent

import (
	"sort"
	"sync"

	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/proxy"
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

type ConsistentHash struct {
	nodes       *sync.Map
	hashFunc    func([]byte) uint32
	numbers     hashRing
	virtualRepl int
}

func NewConsistentHash(replicas int, hashFunc func([]byte) uint32) *ConsistentHash {
	return &ConsistentHash{
		nodes:       &sync.Map{},
		virtualRepl: replicas,
		hashFunc:    hashFunc,
	}
}

func (c *ConsistentHash) AddNode(node *Node) {
	for i := 0; i < c.virtualRepl; i++ {
		hash := c.hashFunc([]byte(string(rune(node.Id+i)) + node.Addr))
		c.nodes.Store(hash, node)
		c.numbers = append(c.numbers, hash)
	}
	sort.Sort(c.numbers)
}

func (c *ConsistentHash) RemoveNode(node *Node) {
	for i := 0; i < c.virtualRepl; i++ {
		hash := c.hashFunc([]byte(string(rune(node.Id+i)) + node.Addr))
		c.nodes.Delete(hash)
		index, err := helper.FindIndex(c.numbers, hash)
		if err == nil {
			c.numbers = helper.Remove(c.numbers, index)
		}
	}
	sort.Sort(c.numbers)
}

func (c *ConsistentHash) GetNode(hash uint32) *Node {
	i := sort.Search(c.numbers.Len(), func(i int) bool { return c.numbers[i] >= hash })

	if i == c.numbers.Len() {
		i = 0
	}

	node, _ := c.nodes.Load(c.numbers[i])

	return node.(*Node)
}
