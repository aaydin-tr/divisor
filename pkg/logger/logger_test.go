package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// captureStreams redirects os.Stderr and os.Stdout to files around fn, so a
// test asserts on what the process would actually emit on each stream. zap
// resolves its "stderr" sink when the logger is built, so InitLogger must be
// called inside fn.
func captureStreams(t *testing.T, fn func()) (stderr, stdout string) {
	t.Helper()

	dir := t.TempDir()
	stderrFile, err := os.Create(filepath.Join(dir, "stderr"))
	require.NoError(t, err)
	stdoutFile, err := os.Create(filepath.Join(dir, "stdout"))
	require.NoError(t, err)

	originalStderr, originalStdout := os.Stderr, os.Stdout
	os.Stderr, os.Stdout = stderrFile, stdoutFile
	defer func() {
		os.Stderr, os.Stdout = originalStderr, originalStdout
		stderrFile.Close()
		stdoutFile.Close()
	}()

	fn()
	zap.L().Sync() //nolint:errcheck

	stderrContent, err := os.ReadFile(stderrFile.Name())
	require.NoError(t, err)
	stdoutContent, err := os.ReadFile(stdoutFile.Name())
	require.NoError(t, err)
	return string(stderrContent), string(stdoutContent)
}

func TestInitLoggerEmitsJSONOnStderrOnly(t *testing.T) {
	stderr, stdout := captureStreams(t, func() {
		InitLogger(config.Logging{Format: config.LoggingFormatJSON, Level: "info"})
		zap.S().Info("hello json")
	})

	assert.Empty(t, stdout)
	line := strings.TrimSpace(stderr)
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &entry), "app log line is not JSON: %q", line)
	assert.Equal(t, "hello json", entry["msg"])
	assert.Equal(t, "info", entry["level"])
	assert.NotEmpty(t, entry["time"])
}

func TestInitLoggerConsoleFormatIsHumanReadableOnStderr(t *testing.T) {
	stderr, stdout := captureStreams(t, func() {
		InitLogger(config.Logging{Format: config.LoggingFormatConsole, Level: "info"})
		zap.S().Info("hello console")
	})

	assert.Empty(t, stdout)
	line := strings.TrimSpace(stderr)
	assert.Contains(t, line, "hello console")
	assert.Contains(t, line, "INFO")
	var entry map[string]any
	assert.Error(t, json.Unmarshal([]byte(line), &entry), "console output should not be JSON")
}

func TestInitLoggerHonorsConfiguredLevel(t *testing.T) {
	t.Run("warn suppresses info", func(t *testing.T) {
		stderr, _ := captureStreams(t, func() {
			InitLogger(config.Logging{Format: config.LoggingFormatJSON, Level: "warn"})
			zap.S().Info("too quiet")
			zap.S().Warn("loud enough")
		})
		assert.NotContains(t, stderr, "too quiet")
		assert.Contains(t, stderr, "loud enough")
	})

	t.Run("debug enables debug output", func(t *testing.T) {
		stderr, _ := captureStreams(t, func() {
			InitLogger(config.Logging{Format: config.LoggingFormatJSON, Level: "debug"})
			zap.S().Debug("debug detail")
		})
		assert.Contains(t, stderr, "debug detail")
	})
}

func TestInitDefaultLoggerIsJSONAtInfoOnStderr(t *testing.T) {
	stderr, stdout := captureStreams(t, func() {
		InitDefaultLogger()
		zap.S().Debug("hidden")
		zap.S().Info("early failure")
	})

	assert.Empty(t, stdout)
	assert.NotContains(t, stderr, "hidden")
	line := strings.TrimSpace(stderr)
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &entry), "default logger line is not JSON: %q", line)
	assert.Equal(t, "early failure", entry["msg"])
}
