package consistent

import (
	"math"
	"strconv"
	"testing"

	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/helper"
)

func TestNewConsistentHash(t *testing.T) {
	replicas := 3
	hashFunc := func([]byte) uint32 {
		return uint32(0)
	}
	ch := NewConsistentHash(replicas, hashFunc)

	if ch.virtualRepl != replicas {
		t.Errorf("Expected virtualRepl to be %d, got %d", replicas, ch.virtualRepl)
	}
}

func TestAddNode(t *testing.T) {
	ch := NewConsistentHash(3, func([]byte) uint32 {
		return uint32(0)
	})

	node := &Node{
		Proxy: &proxy.ProxyClient{Addr: "127.0.0.1:8080"},
		Id:    1,
	}

	ch.AddNode(node)

	ring := ch.ring.Load()
	if len(ring.numbers) != 3 {
		t.Errorf("Expected numbers to have length 3, got %d", len(ring.numbers))
	}

	for i := 0; i < 3; i++ {
		_, ok := ring.nodes[ring.numbers[i]]
		if !ok {
			t.Errorf("Expected node with hash %d to be stored in nodes", ring.numbers[i])
		}
	}
}

func TestRemoveNode(t *testing.T) {
	ch := NewConsistentHash(3, func([]byte) uint32 {
		return uint32(0)
	})

	node := &Node{
		Proxy: &proxy.ProxyClient{Addr: "127.0.0.1:8080"},
		Id:    1,
	}

	ch.AddNode(node)
	ch.RemoveNode(node)

	ring := ch.ring.Load()
	if len(ring.numbers) != 0 {
		t.Errorf("Expected numbers to have length 0, got %d", len(ring.numbers))
	}

	for i := 0; i < 3; i++ {
		_, ok := ring.nodes[uint32(i)]
		if ok {
			t.Errorf("Expected node with hash %d not to be stored in nodes", i)
		}
	}
}

func TestGetNode(t *testing.T) {
	// Create a ConsistentHash struct with replicas 2 and a dummy hash function
	ch := NewConsistentHash(1, func(b []byte) uint32 {
		return uint32(len(b))
	})

	// Add two nodes to the ConsistentHash
	node1 := &Node{
		Proxy: &proxy.ProxyClient{},
		Id:    1,
		Addr:  "localhost:8080",
	}
	node2 := &Node{
		Proxy: &proxy.ProxyClient{},
		Id:    2,
		Addr:  "localhost:80",
	}
	ch.AddNode(node1)
	ch.AddNode(node2)

	// Test cases
	testCases := []struct {
		hash         uint32
		expectedNode *Node
	}{
		{hash: ch.hashFunc(virtualNodeKey(node1, 0)), expectedNode: node1},
		{hash: ch.hashFunc(virtualNodeKey(node2, 0)), expectedNode: node2},
		{hash: ch.hashFunc(virtualNodeKey(node1, 0)), expectedNode: node1},
		{hash: 16, expectedNode: node2},
	}

	for _, tc := range testCases {
		node := ch.GetNode(tc.hash)
		if node.Addr != tc.expectedNode.Addr {
			t.Errorf("For hash %d, expected node %v but got %v", tc.hash, tc.expectedNode.Addr, node.Addr)
		}
	}
}

func TestGetNodeEmptyRing(t *testing.T) {
	ch := NewConsistentHash(3, func(b []byte) uint32 {
		return uint32(len(b))
	})

	if node := ch.GetNode(42); node != nil {
		t.Errorf("Expected nil node from empty ring, got %v", node)
	}
}

func TestGetNodeConcurrentWithAddRemove(t *testing.T) {
	ch := NewConsistentHash(9, helper.HashFunc)
	nodeA := &Node{Proxy: &proxy.ProxyClient{}, Id: 0, Addr: "localhost:8080"}
	nodeB := &Node{Proxy: &proxy.ProxyClient{}, Id: 1, Addr: "localhost:80"}
	ch.AddNode(nodeA)
	ch.AddNode(nodeB)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			ch.RemoveNode(nodeA)
			ch.AddNode(nodeA)
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
			for i := uint32(0); i < 8; i++ {
				if node := ch.GetNode(i * 500000000); node == nil {
					t.Fatal("GetNode returned nil while the ring was not empty")
				}
			}
		}
	}
}

func BenchmarkGetNode(b *testing.B) {
	ch := NewConsistentHash(100, helper.HashFunc)
	for i := 0; i < 10; i++ {
		ch.AddNode(&Node{Proxy: &proxy.ProxyClient{}, Id: i, Addr: "localhost:" + strconv.Itoa(8000+i)})
	}

	b.RunParallel(func(pb *testing.PB) {
		h := uint32(0)
		for pb.Next() {
			h += 2654435761
			ch.GetNode(h)
		}
	})
}

// The same address listed twice in config: two Nodes, same Addr, different Ids.
func TestNodesWithSameAddrKeepSeparateVirtualNodes(t *testing.T) {
	const replicas = 4
	ch := NewConsistentHash(replicas, helper.HashFunc)
	first := &Node{Id: 0, Addr: "127.0.0.1:8080", Proxy: &proxy.ProxyClient{Addr: "127.0.0.1:8080"}}
	second := &Node{Id: 1, Addr: "127.0.0.1:8080", Proxy: &proxy.ProxyClient{Addr: "127.0.0.1:8080"}}

	ch.AddNode(first)
	ch.AddNode(second)

	ring := ch.ring.Load()
	if got := len(ring.numbers); got != 2*replicas {
		t.Fatalf("expected %d virtual nodes, got %d", 2*replicas, got)
	}
	if got := len(ring.nodes); got != 2*replicas {
		t.Fatalf("expected %d distinct positions, got %d", 2*replicas, got)
	}

	ch.RemoveNode(first)

	ring = ch.ring.Load()
	if got := len(ring.numbers); got != replicas {
		t.Fatalf("expected the twin to keep its %d virtual nodes, got %d", replicas, got)
	}
	for _, hash := range ring.numbers {
		if ring.nodes[hash] != second {
			t.Fatalf("position %d no longer routes to the remaining twin", hash)
		}
	}
	const samples, sampleStride = 1000, math.MaxUint32 / 1000
	for i := 0; i < samples; i++ {
		if got := ch.GetNode(uint32(i * sampleStride)); got != second {
			t.Fatalf("GetNode(%d) = %v, want the remaining twin", i, got)
		}
	}
}
