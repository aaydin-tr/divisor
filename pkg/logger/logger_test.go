package logger

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestInitLoggerWritesFile(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "divisor.log")

	InitLogger(logFile)
	zap.S().Info("hello")
	zap.L().Sync() //nolint:errcheck

	content, err := os.ReadFile(logFile)
	assert.Nil(t, err)
	assert.Contains(t, string(content), "hello")
}

func TestInitLoggerFallsBackToStdoutOnUnopenableSink(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "no-such-dir", "divisor.log")

	assert.NotPanics(t, func() {
		InitLogger(logFile)
		zap.S().Info("still alive")
	})
	assert.NotNil(t, zap.L())
}

func TestLogFolderFor(t *testing.T) {
	assert.Equal(t, "/var/log/divisor/", logFolderFor("linux", ""))
	assert.Equal(t, "/var/log/divisor/", logFolderFor("darwin", "ignored"))
	assert.Equal(t, "C:\\Users\\u\\AppData\\Local\\divisor\\", logFolderFor("windows", "C:\\Users\\u\\AppData\\Local"))
	assert.Equal(t, "", logFolderFor("windows", ""))
}

func TestCreateLogDirIfNotExist(t *testing.T) {
	t.Run("existing dir", func(t *testing.T) {
		assert.Nil(t, createLogDirIfNotExist(t.TempDir()))
	})

	t.Run("creates missing dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "logs")
		assert.Nil(t, createLogDirIfNotExist(dir))
		info, err := os.Stat(dir)
		assert.Nil(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("surfaces stat errors other than not-exist", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs Unix permission semantics as a non-root user")
		}
		parent := t.TempDir()
		locked := filepath.Join(parent, "locked")
		assert.Nil(t, os.Mkdir(locked, 0o755))
		assert.Nil(t, os.Chmod(parent, 0o000))
		t.Cleanup(func() { os.Chmod(parent, 0o755) }) //nolint:errcheck

		assert.Error(t, createLogDirIfNotExist(filepath.Join(locked, "sub")))
	})
}
