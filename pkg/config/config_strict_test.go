package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// The Kubernetes example embeds its divisor config in a ConfigMap; kubelet
// probes reach the monitoring server on the pod IP, so it must not bind localhost.
func TestKubernetesExampleConfigMapHoldsAValidConfig(t *testing.T) {
	manifests, err := filepath.Glob("../../examples/kubernetes/*.yaml")
	require.NoError(t, err)

	embeddedConfigs := map[string]string{}
	for _, manifest := range manifests {
		for name, configYAML := range divisorConfigsInConfigMaps(t, manifest) {
			embeddedConfigs[filepath.Base(manifest)+"/"+name] = configYAML
		}
	}
	require.NotEmpty(t, embeddedConfigs, "no ConfigMap with a config.yaml key under examples/kubernetes")

	for name, configYAML := range embeddedConfigs {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig([]byte(configYAML))
			require.NoError(t, err)
			require.NoError(t, cfg.PrepareConfig())
			assert.Equal(t, "0.0.0.0", cfg.Monitoring.Host)
		})
	}
}

// The Helm chart and the Kubernetes example ship one default config; a fix to
// one that misses the other would leave users with two behaviors.
func TestHelmChartConfigMatchesTheKubernetesExample(t *testing.T) {
	values, err := os.ReadFile("../../charts/divisor/values.yaml")
	require.NoError(t, err)
	var chartValues struct {
		Config map[string]any `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal(values, &chartValues))
	chartConfigYAML, err := yaml.Marshal(chartValues.Config)
	require.NoError(t, err)

	chartConfig, err := ParseConfig(chartConfigYAML)
	require.NoError(t, err)
	require.NoError(t, chartConfig.PrepareConfig())

	exampleConfigYAML, ok := divisorConfigsInConfigMaps(t, "../../examples/kubernetes/configmap.yaml")["divisor-config"]
	require.True(t, ok, "examples/kubernetes/configmap.yaml has no divisor-config ConfigMap with a config.yaml key")
	var exampleConfig map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(exampleConfigYAML), &exampleConfig))

	assert.Equal(t, exampleConfig, chartValues.Config)
}

func divisorConfigsInConfigMaps(t *testing.T, manifestPath string) map[string]string {
	t.Helper()
	manifest, err := os.Open(manifestPath)
	require.NoError(t, err)
	defer manifest.Close()

	configs := map[string]string{}
	decoder := yaml.NewDecoder(manifest)
	for {
		var resource struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			return configs
		}
		require.NoError(t, err, manifestPath)
		if configYAML, ok := resource.Data["config.yaml"]; resource.Kind == "ConfigMap" && ok {
			configs[resource.Metadata.Name] = configYAML
		}
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
