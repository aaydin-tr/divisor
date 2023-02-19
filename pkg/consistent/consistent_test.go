package consistent

import (
	"testing"

	"github.com/aaydin-tr/balancer/proxy"
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

	if len(ch.numbers) != 3 {
		t.Errorf("Expected numbers to have length 3, got %d", len(ch.numbers))
	}

	for i := 0; i < 3; i++ {
		_, ok := ch.nodes.Load(ch.numbers[i])
		if !ok {
			t.Errorf("Expected node with hash %d to be stored in nodes", ch.numbers[i])
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

	if len(ch.numbers) != 0 {
		t.Errorf("Expected numbers to have length 0, got %d", len(ch.numbers))
	}

	for i := 0; i < 3; i++ {
		_, ok := ch.nodes.Load(uint32(i))
		if ok {
			t.Errorf("Expected node with hash %d not to be stored in nodes", i)
		}
	}
}

func TestGetNode(t *testing.T) {
	// Create a ConsistentHash struct with replicas 2 and a dummy hash function
	ch := NewConsistentHash(2, func(b []byte) uint32 {
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
		{hash: ch.hashFunc([]byte(string(rune(node1.Id+0)) + node1.Addr)), expectedNode: node1},
		{hash: ch.hashFunc([]byte(string(rune(node2.Id+0)) + node2.Addr)), expectedNode: node2},
		{hash: ch.hashFunc([]byte(string(rune(node1.Id+0)) + node1.Addr)), expectedNode: node1},
	}

	for _, tc := range testCases {
		node := ch.GetNode(tc.hash)
		if node.Addr != tc.expectedNode.Addr {
			t.Errorf("For hash %d, expected node %v but got %v", tc.hash, tc.expectedNode.Addr, node.Addr)
		}
	}
}
