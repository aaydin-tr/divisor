package logger

import (
	"errors"
	"os"
	"runtime"
)

// GetLogFile returns the platform log file path, falling back to
// ./divisor.log when the platform log directory is unknown or unusable.
func GetLogFile() string {
	logDir := getLogFolder()
	if logDir == "" {
		return "./divisor.log"
	}

	if err := createLogDirIfNotExist(logDir); err != nil {
		return "./divisor.log"
	}

	return logDir + "divisor.log"
}

func getLogFolder() string {
	return logFolderFor(runtime.GOOS, os.Getenv("LocalAppData"))
}

func logFolderFor(goos, localAppData string) string {
	if goos == "windows" {
		if localAppData == "" {
			return ""
		}
		return localAppData + "\\divisor\\"
	}
	return "/var/log/divisor/"
}

// createLogDirIfNotExist treats any stat error other than "not exist" (e.g.
// permission denied) as failure, so an unusable directory falls back instead
// of being handed to the log sink.
func createLogDirIfNotExist(logDir string) error {
	_, err := os.Stat(logDir)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Mkdir(logDir, os.ModePerm)
}
