package helper

import (
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
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

func Remove[T any](s []T, index int) []T {
	return append(s[:index], s[index+1:]...)
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

func FindIndex[T comparable](s []T, value T) (int, error) {
	for i, elem := range s {
		if elem == value {
			return i, nil
		}
	}

	return 0, errors.New("not found in slice")
}

func GetLogFile() string {
	logDir := GetLogFolder()
	err := CreateLogDirIfNotExist(logDir)
	if err != nil {
		return "./divisor.log"
	}

	return logDir + "divisor.log"
}

func GetLogFolder() string {
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = os.Getenv("LocalAppData") + "\\divisor\\"
		if dir == "" {
			return ""
		}
	default: // Unix
		dir = "/var/log/divisor/"
	}

	return dir
}

func CreateLogDirIfNotExist(logDir string) error {
	if _, err := os.Stat(logDir); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(logDir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	return nil
}

func IsFileExist(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return errors.New(fmt.Sprintf("%s file does not exist", file))
	}
	if info.IsDir() {
		return errors.New("Provided a dir not file")
	}
	return nil
}
