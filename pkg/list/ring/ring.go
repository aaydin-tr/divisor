package ring

import "github.com/aaydin-tr/balancer/proxy"

type Node struct {
	Proxy *proxy.ProxyClient
	Next  *Node
}

type List struct {
	Len  uint
	Head *Node
	Tail *Node
}

func NewRingLinkedList() *List {
	return &List{}
}

func (l *List) AddToTail(value *proxy.ProxyClient) {
	newNode := &Node{Proxy: value}

	if l.Len == 0 {
		l.Head = newNode
		l.Tail = newNode
	} else {
		oldTail := l.Tail
		oldTail.Next = newNode
		l.Tail = newNode
	}
	l.Tail.Next = l.Head
	l.Len++
}
