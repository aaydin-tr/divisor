package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every shipped example must parse under strict decoding: an example with
// a stale key would teach users a config that divisor now rejects.
func TestExamplesParseUnderStrictKeys(t *testing.T) {
	examples, err := filepath.Glob("../../examples/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, examples)
	for _, example := range examples {
		t.Run(filepath.Base(example), func(t *testing.T) {
			_, err := ParseConfigFile(example)
			assert.NoError(t, err)
		})
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestParseConfigFileRejectsUnknownKeys(t *testing.T) {
	t.Run("top-level typo", func(t *testing.T) {
		_, err := ParseConfigFile(writeConfig(t, "port: 8000\nbakends:\n  - url: localhost:8080\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bakends")
	})

	t.Run("nested typo", func(t *testing.T) {
		_, err := ParseConfigFile(writeConfig(t, "port: 8000\nbackends:\n  - url: localhost:8080\nserver:\n  proxy_timout: 5s\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proxy_timout")
	})

	t.Run("removed key", func(t *testing.T) {
		_, err := ParseConfigFile(writeConfig(t, "port: 8000\nbackends:\n  - url: localhost:8080\nserver:\n  disable_header_names_normalizing: true\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_header_names_normalizing")
	})

	t.Run("middleware config map stays free-form", func(t *testing.T) {
		cfg, err := ParseConfigFile(writeConfig(t, "port: 8000\nbackends:\n  - url: localhost:8080\nmiddlewares:\n  - name: m\n    code: x\n    config:\n      anything: goes\n      nested:\n        too: 1\n"))
		require.NoError(t, err)
		assert.Equal(t, "goes", cfg.Middlewares[0].Config["anything"])
	})

	t.Run("empty file", func(t *testing.T) {
		_, err := ParseConfigFile(writeConfig(t, ""))
		assert.ErrorIs(t, err, ErrConfigFileEmpty)
	})
}

func TestPrepareServerHttpVersion(t *testing.T) {
	for _, accepted := range []string{"", "http1", "http1.1"} {
		t.Run("accepts "+accepted, func(t *testing.T) {
			s := Server{HttpVersion: accepted}
			require.NoError(t, s.prepareServer())
			assert.Equal(t, Http1, s.HttpVersion)
		})
	}
	for _, rejected := range []string{"HTTP2", "h2", "http/2", "http3", "http1.0"} {
		t.Run("rejects "+rejected, func(t *testing.T) {
			s := Server{HttpVersion: rejected}
			err := s.prepareServer()
			assert.ErrorIs(t, err, ErrInvalidHttpVersion)
			assert.Contains(t, err.Error(), rejected)
		})
	}
}
