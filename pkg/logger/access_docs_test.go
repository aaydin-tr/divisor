package logger

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const observabilityGuidePath = "../../docs/observability.md"

const accessLogFieldsHeading = "### Access log fields"

var documentedFieldRow = regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")

func accessLogFieldsDocumentedInObservabilityGuide(t *testing.T) []string {
	t.Helper()
	guide, err := os.ReadFile(observabilityGuidePath)
	require.NoError(t, err)

	guideText := strings.ReplaceAll(string(guide), "\r\n", "\n")
	_, section, found := strings.Cut(guideText, accessLogFieldsHeading+"\n")
	require.True(t, found, "%s has no %q section", observabilityGuidePath, accessLogFieldsHeading)
	if nextHeading := strings.Index(section, "\n#"); nextHeading >= 0 {
		section = section[:nextHeading]
	}

	var fields []string
	for _, row := range documentedFieldRow.FindAllStringSubmatch(section, -1) {
		fields = append(fields, row[1])
	}
	return fields
}

func accessLogFieldsEmittedForAShortCircuitedRequest(t *testing.T) []string {
	t.Helper()
	disableAccessLoggerOnCleanup(t)
	_, stdout := captureStreams(t, func() {
		InitAccessLogger(true)
		entry := fullAccessLogEntry()
		entry.ShortCircuit = true
		LogAccess(entry)
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &line))
	fields := make([]string, 0, len(line))
	for field := range line {
		fields = append(fields, field)
	}
	return fields
}

func TestObservabilityGuideDocumentsEveryAccessLogField(t *testing.T) {
	assert.ElementsMatch(t,
		accessLogFieldsEmittedForAShortCircuitedRequest(t),
		accessLogFieldsDocumentedInObservabilityGuide(t),
		"the Access log field table in %s must list exactly the fields the Access log emits", observabilityGuidePath)
}
