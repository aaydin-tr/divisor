package ring

import (
	"testing"

	"github.com/aaydin-tr/balancer/http"
)

func emptyList() *List {
	return &List{}
}

var cases = []struct {
	value *http.HTTPClient
}{
	{&http.HTTPClient{}},
	{&http.HTTPClient{}},
	{&http.HTTPClient{}},
	{&http.HTTPClient{}},
}

func TestNewLinkedList(t *testing.T) {
	list := NewRingLinkedList()
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
		list.AddToTail(c.value)

		if list.Len != uint(i)+1 {
			t.Errorf("Expected list.Len to be %d, got %d", i, list.Len)
		}
	}

	if list.Head != list.Tail.Next {
		t.Errorf("Expected list.Head equal to be list.Tail.Next, got %v", list.Tail.Next)
	}
}
