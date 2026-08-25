package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
)

// divisorStreams fetches the divisor container's stdout and stderr
// separately: the logging contract is per-stream (app logs on stderr, the
// Access log on stdout), so merged logs cannot assert it.
func divisorStreams(t *testing.T, res *dockertest.Resource) (stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	err := pool.Client.Logs(dc.LogsOptions{
		Container:    res.Container.ID,
		OutputStream: &outBuf,
		ErrorStream:  &errBuf,
		Stdout:       true,
		Stderr:       true,
	})
	if err != nil {
		t.Fatalf("fetching divisor container streams: %v", err)
	}
	return outBuf.String(), errBuf.String()
}

func nonBlankLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// App logs default to JSON on stderr; stdout stays empty (it is reserved for
// the Access log, off by default).
func TestApplicationLogsAreJSONOnStderrByDefault(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "applogs-json",
		Backends: []BackendSpec{{ID: "b1"}},
	})

	stdout, stderr := divisorStreams(t, s.Divisor)

	if len(nonBlankLines(stdout)) > 0 {
		t.Errorf("stdout should be silent (reserved for the Access log), got:\n%s", stdout)
	}

	lines := nonBlankLines(stderr)
	if len(lines) == 0 {
		t.Fatalf("no app log lines arrived on stderr")
	}
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("app log line on stderr is not JSON: %q", line)
		}
		if entry["level"] == nil || entry["msg"] == nil {
			t.Errorf("app log line is missing level/msg fields: %q", line)
		}
	}
}

// With logging.access_log on, stdout carries exactly one JSON line per
// answered request with the fixed field set, and nothing else: app logs stay
// on stderr, so each stream remains homogeneous.
func TestAccessLogOneJSONLinePerRequestOnStdout(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:      "accesslog",
		Backends:  []BackendSpec{{ID: "b1"}},
		AccessLog: true,
	})

	// startScenario's readiness probing already answered requests, so count
	// the pre-existing lines and assert on the delta.
	stdoutBefore, _ := divisorStreams(t, s.Divisor)
	linesBefore := len(nonBlankLines(stdoutBefore))

	const requestCount = 5
	for i := 0; i < requestCount; i++ {
		res, err := s.Request(http.MethodGet, fmt.Sprintf("/access-log-test?n=%d", i), nil, nil)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("request %d got status %d", i, res.StatusCode)
		}
	}

	var newLines []string
	eventually(t, 10*time.Second, "access log lines arrive on stdout", func() error {
		stdout, _ := divisorStreams(t, s.Divisor)
		newLines = nonBlankLines(stdout)[linesBefore:]
		if len(newLines) != requestCount {
			return fmt.Errorf("got %d new stdout lines, want %d", len(newLines), requestCount)
		}
		return nil
	})

	backendAddr := s.Backend("b1").Name + ":" + containerPort
	var previousSequence float64
	for i, line := range newLines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("access log line is not JSON: %q", line)
		}
		for _, field := range []string{"time", "client_ip", "method", "path", "status", "backend", "duration_ms", "bytes_out", "request_seq"} {
			if _, ok := entry[field]; !ok {
				t.Fatalf("access log line is missing %q: %q", field, line)
			}
		}
		if _, ok := entry["msg"]; ok {
			t.Errorf("access log line carries an app-log msg field: %q", line)
		}
		if entry["method"] != "GET" || entry["status"] != float64(200) || entry["backend"] != backendAddr {
			t.Errorf("unexpected access log values in %q", line)
		}
		if entry["path"] != "/access-log-test" {
			t.Errorf("path should carry no query string, got %v", entry["path"])
		}
		sequence, ok := entry["request_seq"].(float64)
		if !ok || (i > 0 && sequence != previousSequence+1) {
			t.Errorf("request_seq should increase by one per request on a single backend, line %d: %q", i, line)
		}
		previousSequence = sequence
	}
}

// logging.format: console produces human-readable (non-JSON) app logs, still
// on stderr only.
func TestApplicationLogsConsoleFormat(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:          "applogs-console",
		Backends:      []BackendSpec{{ID: "b1"}},
		LoggingFormat: "console",
	})

	stdout, stderr := divisorStreams(t, s.Divisor)

	if len(nonBlankLines(stdout)) > 0 {
		t.Errorf("stdout should be silent (reserved for the Access log), got:\n%s", stdout)
	}

	lines := nonBlankLines(stderr)
	if len(lines) == 0 {
		t.Fatalf("no app log lines arrived on stderr")
	}
	if !strings.Contains(stderr, "INFO") {
		t.Errorf("console-format logs should carry a human-readable level, got:\n%s", stderr)
	}
	// Lines logged before the config is validated use the default JSON
	// logger (spec: startup ordering unchanged), so only lines after the
	// re-initialization are console-formatted — the last line always is.
	lastLine := lines[len(lines)-1]
	var entry map[string]any
	if err := json.Unmarshal([]byte(lastLine), &entry); err == nil {
		t.Errorf("console-format log line unexpectedly parsed as JSON: %q", lastLine)
	}
}
