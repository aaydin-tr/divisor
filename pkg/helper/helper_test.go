package helper_test

import (
	"bytes"
	"testing"

	"github.com/aaydin-tr/divisor/pkg/helper"
)

func TestContains(t *testing.T) {
	testCases := []struct {
		slice    []string
		elem     string
		expected bool
	}{
		{[]string{"hello", "world"}, "hello", true},
		{[]string{"hello", "world"}, "goodbye", false},
		{[]string{}, "hello", false},
	}

	for _, testCase := range testCases {
		result := helper.Contains(testCase.slice, testCase.elem)
		if result != testCase.expected {
			t.Errorf("For slice %v and elem %s, expected %t but got %t", testCase.slice, testCase.elem, testCase.expected, result)
		}
	}
}

func TestB2S(t *testing.T) {
	testCases := []struct {
		bytes    []byte
		expected string
	}{
		{[]byte("hello"), "hello"},
		{[]byte("world"), "world"},
		{[]byte{}, ""},
	}

	for _, testCase := range testCases {
		result := helper.B2S(testCase.bytes)
		if result != testCase.expected {
			t.Errorf("For bytes %v, expected %s but got %s", testCase.bytes, testCase.expected, result)
		}
	}
}

func TestS2B(t *testing.T) {
	testCases := []struct {
		str      string
		expected []byte
	}{
		{"hello", []byte("hello")},
		{"world", []byte("world")},
		{"", []byte{}},
	}

	for _, testCase := range testCases {
		result := helper.S2B(testCase.str)
		if !bytes.Equal(result, testCase.expected) {
			t.Errorf("For string %s, expected %v but got %v", testCase.str, testCase.expected, result)
		}
	}
}

func TestHashFunc(t *testing.T) {
	testCases := []struct {
		input    []byte
		expected uint32
	}{
		{[]byte("hello"), 907060870},
		{[]byte("world"), 980881731},
		{[]byte(""), 0},
		{[]byte("golang"), 2937857443},
	}

	for _, testCase := range testCases {
		result := helper.HashFunc(testCase.input)
		if result != testCase.expected {
			t.Errorf("For input %v, expected %d but got %d", testCase.input, testCase.expected, result)
		}
	}
}

func TestRemoveByValue(t *testing.T) {
	testCases := []struct {
		slice    []int
		value    int
		expected []int
	}{
		{[]int{1, 2, 3, 2}, 2, []int{1, 3}},
		{[]int{1, 2, 3, 4, 2}, 2, []int{1, 3, 4}},
		{[]int{1, 1}, 1, []int{}},
	}

	for _, testCase := range testCases {
		result := helper.RemoveByValue(testCase.slice, testCase.value)
		if len(result) != len(testCase.expected) {
			t.Errorf("For slice %v and value %d, expected length %d but got %d", testCase.slice, testCase.value, len(testCase.expected), len(result))
		}
		for i := range result {
			if result[i] != testCase.expected[i] {
				t.Errorf("For slice %v and value %d, expected %v but got %v", testCase.slice, testCase.value, testCase.expected, result)
				break
			}
		}
	}
}

func TestIsClosed(t *testing.T) {
	ch := make(chan struct{})
	if helper.IsClosed(ch) {
		t.Error("IsClosed reported an open channel as closed")
	}

	close(ch)
	if !helper.IsClosed(ch) {
		t.Error("IsClosed reported a closed channel as open")
	}
	if !helper.IsClosed(ch) {
		t.Error("IsClosed consumed the close signal")
	}
}
