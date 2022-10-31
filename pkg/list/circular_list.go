package circular_list

import "github.com/aaydin-tr/balancer/http"

type Node struct {
	Proxy *http.HTTPClient
	Next  *Node
}

type List struct {
	Len  uint
	Head *Node
	Tail *Node
}

func NewCircularLinkedList() *List {
	return &List{}
}

func (l *List) AddToTail(value *http.HTTPClient) {
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
