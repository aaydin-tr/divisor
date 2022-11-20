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

func NewWLinkedList() *List {
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
	if l.Head == nil {
		return
	}

	l.Head = mergeSort(l.Head)
}

func getMid(head *Node) *Node {
	slow, fast := head, head.Next

	for fast != nil && fast.Next != nil {
		slow = slow.Next
		fast = fast.Next.Next
	}
	return slow
}

func mergeSort(head *Node) *Node {
	if head == nil || head.Next == nil {
		return head
	}

	left := head
	right := getMid(head)

	tmp := right.Next
	right.Next = nil
	right = tmp

	left = mergeSort(left)
	right = mergeSort(right)

	return merge(left, right)
}

func merge(list1, list2 *Node) *Node {
	var result = &Node{}

	if list1 == nil {
		return list2
	}

	if list2 == nil {
		return list1
	}

	if list1.Weight >= list2.Weight {
		result = list1
		result.Next = merge(list1.Next, list2)
	} else {
		result = list2
		result.Next = merge(list1, list2.Next)
	}

	return result
}
