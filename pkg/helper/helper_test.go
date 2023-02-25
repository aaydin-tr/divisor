package helper_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aaydin-tr/balancer/pkg/helper"
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

func TestB2s(t *testing.T) {
	testCases := []struct {
		bytes    []byte
		expected string
	}{
		{[]byte("hello"), "hello"},
		{[]byte("world"), "world"},
		{[]byte{}, ""},
	}

	for _, testCase := range testCases {
		result := helper.B2s(testCase.bytes)
		if result != testCase.expected {
			t.Errorf("For bytes %v, expected %s but got %s", testCase.bytes, testCase.expected, result)
		}
	}
}

func TestS2b(t *testing.T) {
	testCases := []struct {
		str      string
		expected []byte
	}{
		{"hello", []byte("hello")},
		{"world", []byte("world")},
		{"", []byte{}},
	}

	for _, testCase := range testCases {
		result := helper.S2b(testCase.str)
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

func TestRemove(t *testing.T) {
	testCases := []struct {
		slice    []int
		index    int
		expected []int
	}{
		{[]int{1, 2, 3}, 1, []int{1, 3}},
		{[]int{1, 2, 3, 4}, 0, []int{2, 3, 4}},
		{[]int{1}, 0, []int{}},
	}

	for _, testCase := range testCases {
		result := helper.Remove(testCase.slice, testCase.index)
		if len(result) != len(testCase.expected) {
			t.Errorf("For slice %v and index %d, expected length %d but got %d", testCase.slice, testCase.index, len(testCase.expected), len(result))
		}
		for i := range result {
			if result[i] != testCase.expected[i] {
				t.Errorf("For slice %v and index %d, expected %v but got %v", testCase.slice, testCase.index, testCase.expected, result)
				break
			}
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

func TestFindIndex(t *testing.T) {
	testCases := []struct {
		slice    []int
		value    int
		expected int
		err      error
	}{
		{[]int{1, 2, 3}, 2, 1, nil},
		{[]int{1, 2, 3, 4}, 4, 3, nil},
		{[]int{1}, 2, 0, errors.New("not found in slice")},
		{[]int{1, 2, 3, 4, 2}, 5, 0, errors.New("not found in slice")},
	}

	for _, testCase := range testCases {
		result, err := helper.FindIndex(testCase.slice, testCase.value)
		if result != testCase.expected {
			t.Errorf("For slice %v and value %d, expected %d but got %d", testCase.slice, testCase.value, testCase.expected, result)
		}

		if err != nil && errors.Is(err, testCase.err) {
			t.Errorf("For slice %v and value %d, expected error %v but got %v", testCase.slice, testCase.value, testCase.err, err)
		}
	}
}
