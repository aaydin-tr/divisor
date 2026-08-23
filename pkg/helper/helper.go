package helper

import (
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"unsafe"
)

func Contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func B2S(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func S2B(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func HashFunc(b []byte) uint32 {
	return crc32.ChecksumIEEE(b)
}

func RemoveByValue[T comparable](s []T, value T) []T {
	temp := make([]T, 0, len(s))
	for _, elem := range s {
		if elem != value {
			temp = append(temp, elem)
		}
	}
	return temp
}

// IsClosed reports whether ch is closed. Only valid for channels used purely
// as close-only signals: on a channel that carries sent values it consumes one
// and reports it as a close.
func IsClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func IsFileExist(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("%s file does not exist", file)
	}
	if info.IsDir() {
		return errors.New("Provided a dir not file")
	}
	return nil
}
