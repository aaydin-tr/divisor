package logger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// disableAccessLoggerOnCleanup pins the package-global access logger back to disabled
// when the test ends, so tests cannot leak state into each other.
func disableAccessLoggerOnCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { InitAccessLogger(false) })
}

func fullAccessLogEntry() *AccessLogEntry {
	return &AccessLogEntry{
		ClientIP:        "10.0.0.1",
		Method:          "GET",
		Path:            "/api/users",
		Status:          200,
		Backend:         "backend-1:8080",
		Duration:        1500 * time.Microsecond,
		BytesOut:        512,
		RequestSequence: 42,
	}
}

func TestAccessLogWritesOneJSONLineToStdout(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	stderr, stdout := captureStreams(t, func() {
		InitAccessLogger(true)
		LogAccess(fullAccessLogEntry())
	})

	assert.Empty(t, stderr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 1)

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry), "access log line is not JSON: %q", lines[0])
	assert.Equal(t, "10.0.0.1", entry["client_ip"])
	assert.Equal(t, "GET", entry["method"])
	assert.Equal(t, "/api/users", entry["path"])
	assert.Equal(t, float64(200), entry["status"])
	assert.Equal(t, "backend-1:8080", entry["backend"])
	assert.Equal(t, 1.5, entry["duration_ms"])
	assert.Equal(t, float64(512), entry["bytes_out"])
	assert.Equal(t, float64(42), entry["request_seq"])
	assert.NotContains(t, entry, "short_circuit")
	assert.NotContains(t, entry, "msg")
	assert.NotContains(t, entry, "level")
}

func TestAccessLogTimeIsRFC3339MillisUTC(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	_, stdout := captureStreams(t, func() {
		InitAccessLogger(true)
		LogAccess(fullAccessLogEntry())
	})

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entry))
	timeValue, ok := entry["time"].(string)
	require.True(t, ok, "access log line has no time field")
	assert.True(t, strings.HasSuffix(timeValue, "Z"), "access log time %q is not UTC", timeValue)
	_, err := time.Parse("2006-01-02T15:04:05.000Z07:00", timeValue)
	assert.NoError(t, err, "access log time %q is not RFC 3339 with milliseconds", timeValue)
}

func TestAccessLogOmitsBackendFieldsWhenNoBackendInvolved(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	_, stdout := captureStreams(t, func() {
		InitAccessLogger(true)
		LogAccess(&AccessLogEntry{ClientIP: "10.0.0.1", Method: "GET", Path: "/", Status: 503})
	})

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entry))
	assert.NotContains(t, entry, "backend")
	assert.NotContains(t, entry, "request_seq")
	assert.Equal(t, float64(503), entry["status"])
}

func TestAccessLogMarksShortCircuits(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	_, stdout := captureStreams(t, func() {
		InitAccessLogger(true)
		entry := fullAccessLogEntry()
		entry.ShortCircuit = true
		LogAccess(entry)
	})

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entry))
	assert.Equal(t, true, entry["short_circuit"])
}

func TestAccessLogOffByDefaultAndWhenDisabled(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	t.Run("disabled explicitly", func(t *testing.T) {
		_, stdout := captureStreams(t, func() {
			InitAccessLogger(false)
			assert.False(t, AccessLogEnabled())
			LogAccess(fullAccessLogEntry())
		})
		assert.Empty(t, stdout)
	})

	t.Run("enabled reports enabled", func(t *testing.T) {
		captureStreams(t, func() {
			InitAccessLogger(true)
			assert.True(t, AccessLogEnabled())
		})
	})
}

func TestAccessLogStaysJSONWhenAppLogsAreConsole(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	_, stdout := captureStreams(t, func() {
		InitLogger(config.Logging{Format: config.LoggingFormatConsole, Level: "info"})
		InitAccessLogger(true)
		LogAccess(fullAccessLogEntry())
	})

	var entry map[string]any
	assert.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entry),
		"access log must stay JSON regardless of logging.format")
}

func TestReplaceAccessLoggerObservesEntries(t *testing.T) {
	disableAccessLoggerOnCleanup(t)
	observerCore, observed := observer.New(zapcore.InfoLevel)
	restore := ReplaceAccessLogger(zap.New(observerCore))
	defer restore()

	assert.True(t, AccessLogEnabled())
	LogAccess(fullAccessLogEntry())

	require.Equal(t, 1, observed.Len())
	fields := observed.All()[0].ContextMap()
	assert.Equal(t, "backend-1:8080", fields["backend"])
	assert.Equal(t, uint64(42), fields["request_seq"])

	restore()
	assert.False(t, AccessLogEnabled())
}
