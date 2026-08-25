package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
