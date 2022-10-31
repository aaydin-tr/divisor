package w_list

import (
	"github.com/aaydin-tr/balancer/http"
)

type Node struct {
	Proxy  *http.HTTPClient
	Weight uint
	Next   *Node
}

type List struct {
	Len  uint
	Head *Node
	Tail *Node
}

func NewSortedLinkedList() *List {
	return &List{}
}

func (l *List) AddToTail(proxy *http.HTTPClient, weight uint) {
	newNode := &Node{Proxy: proxy, Weight: weight}

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

func (l *List) Sort() {
	prev := l.Head
	curr := l.Head.Next

	for curr != nil {
		if prev.Weight < curr.Weight {
			prev.Next = curr.Next
			curr.Next = l.Head
			l.Head = curr

			curr = prev
		} else {
			prev = curr
		}
		curr = curr.Next
	}

}
