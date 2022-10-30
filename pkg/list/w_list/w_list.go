package w_list

import "github.com/aaydin-tr/balancer/http"

type Node struct {
	Proxy  *http.HTTPClient
	Weight uint
	Name   string
	Next   *Node
}

type List struct {
	Len  uint
	Head *Node
	Tail *Node
}

func NewLinkedList() *List {
	return &List{}
}

func (l *List) AddToTail(proxy *http.HTTPClient, weight uint, name string) {
	newNode := &Node{Proxy: proxy, Weight: weight, Name: name}

	if l.Len == 0 {
		l.Head = newNode
		l.Tail = newNode
	} else {
		oldTail := l.Tail
		oldTail.Next = newNode
		l.Tail = newNode
	}
	l.Len++
}
