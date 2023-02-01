package helper

import (
	"errors"
	"hash/crc32"
	"reflect"
	"unsafe"

	"github.com/aaydin-tr/balancer/pkg/http"
)

func Contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func B2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func S2b(s string) (b []byte) {
	bh := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh.Data = sh.Data
	bh.Cap = sh.Len
	bh.Len = sh.Len
	return b
}

func HashFunc(b []byte) uint32 {
	return crc32.ChecksumIEEE(b)
}

func Remove[T any](s []T, index int) []T {
	return append(s[:index], s[index+1:]...)
}

func RemoveMultipleByValue[T comparable](s []T, value T) []T {
	var temp []T
	for _, elem := range s {
		if elem != value {
			temp = append(temp, elem)
		}
	}
	return temp
}

func FindIndex[T comparable](s []T, value T) (int, error) {
	for i, elem := range s {
		if elem == value {
			return i, nil
		}
	}

	return 0, errors.New("not found in slice")
}

// TODO
func IsHostAlive(url string) bool {
	return http.NewHttpClient().DefaultHealtChecker(url) == 200
}
