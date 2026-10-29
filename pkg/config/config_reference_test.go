package config

import (
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/configuration.md is the operator's whole reference: every key the strict
// parser accepts has a row there, and every row names a key it accepts.
func TestConfigurationReferenceDocumentsEveryKeyTheParserAccepts(t *testing.T) {
	reference, err := os.ReadFile("../../docs/configuration.md")
	require.NoError(t, err)
	documentedKeys := referenceRowsByKey(string(reference))

	assert.ElementsMatch(t, acceptedKeyPaths(reflect.TypeOf(Config{}), ""), slices.Collect(maps.Keys(documentedKeys)))

	for key, row := range documentedKeys {
		assert.Regexp(t, `^\d+\.\d+\.\d+$`, row.since, "since-version of %s", key)
		assert.Equal(t, reloadableKeyPath(key), row.reload == "yes", "Reload column of %s", key)
	}
}

type referenceRow struct {
	since  string
	reload string
}

// A key row is | `key` | type | default | since | reload | description |.
var referenceKeyRow = regexp.MustCompile("(?m)^\\| `([a-z0-9_.\\[\\]]+)` \\|(.*)$")

func referenceRowsByKey(reference string) map[string]referenceRow {
	rows := map[string]referenceRow{}
	for _, match := range referenceKeyRow.FindAllStringSubmatch(reference, -1) {
		cells := strings.Split(match[2], "|")
		if len(cells) < 4 {
			rows[match[1]] = referenceRow{}
			continue
		}
		rows[match[1]] = referenceRow{since: strings.TrimSpace(cells[2]), reload: strings.TrimSpace(cells[3])}
	}
	return rows
}

// yaml.v3 accepts an untagged field under its lowercased name; a list's entry
// keys are named `list[].key`, while a section (struct) is not a key itself.
func acceptedKeyPaths(section reflect.Type, keyPrefix string) []string {
	var keyPaths []string
	for i := range section.NumField() {
		field := section.Field(i)
		yamlName, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if yamlName == "-" {
			continue
		}
		if yamlName == "" {
			yamlName = strings.ToLower(field.Name)
		}
		key := keyPrefix + yamlName
		switch {
		case field.Type.Kind() == reflect.Struct:
			keyPaths = append(keyPaths, acceptedKeyPaths(field.Type, key+".")...)
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct:
			keyPaths = append(keyPaths, key)
			keyPaths = append(keyPaths, acceptedKeyPaths(field.Type.Elem(), key+"[].")...)
		default:
			keyPaths = append(keyPaths, key)
		}
	}
	return keyPaths
}

func reloadableKeyPath(keyPath string) bool {
	segments := strings.Split(strings.ReplaceAll(keyPath, "[]", ""), ".")
	for i := range segments {
		if reloadableKeys[strings.Join(segments[:i+1], ".")] {
			return true
		}
	}
	return false
}
