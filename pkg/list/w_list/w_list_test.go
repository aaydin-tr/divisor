package w_list

import (
	"testing"

	"github.com/aaydin-tr/balancer/http"
)

var cases = []struct {
	proxy  *http.HTTPClient
	weight uint
}{
	{&http.HTTPClient{}, 5},
	{&http.HTTPClient{}, 3},
	{&http.HTTPClient{}, 4},
	{&http.HTTPClient{}, 7},
}

func emptyList() *List {
	return &List{}
}

func TestNewLinkedList(t *testing.T) {
	list := NewWLinkedList()
	if list.Len != 0 {
		t.Errorf("Expected list.Len to be 0, got %d", list.Len)
	}
	if list.Head != nil {
		t.Errorf("Expected list.Head to be nil, got %v", list.Head)
	}
	if list.Tail != nil {
		t.Errorf("Expected list.Tail to be nil, got %v", list.Tail)
	}
}

func TestAddToTail(t *testing.T) {
	list := emptyList()

	for i, c := range cases {
		tail := list.Tail
		head := list.Head

		list.AddToTail(c.proxy, c.weight)

		if list.Len != uint(i)+1 {
			t.Errorf("Expected list.Len to be %d, got %d", i, list.Len)
		}

		if i == 0 {
			if tail != head {
				t.Errorf("Expected tail equal to be head, got tail => %v and head => %v", tail, head)
			}
		} else {
			if tail.Next != list.Tail {
				t.Errorf("Expected old tail next equal to be new tail, got old tail => %v and new tail => %v", tail.Next, list.Tail)
			}
		}

	}
}
