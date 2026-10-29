package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runningConfigYAML = `
port: 8000
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
`

func runningConfig(t *testing.T, yaml string) *Config {
	t.Helper()
	cfg, err := ParseConfig([]byte(yaml))
	require.NoError(t, err)
	require.NoError(t, cfg.PrepareConfig())
	return cfg
}

func TestPlanReloadAppliesBackendSetChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		running         string
		next            string
		kept            []KeptBackend
		added           []int
		removed         []int
		probeSettingsUp bool
	}{
		{
			name:    "unchanged config is a no-op",
			next:    runningConfigYAML,
			kept:    []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
			added:   nil,
			removed: nil,
		},
		{
			name: "an added entry is an added Backend",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
  - url: c:8080
`,
			kept:  []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
			added: []int{2},
		},
		{
			name: "a dropped entry is a removed Backend",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: b:8080
`,
			kept:    []KeptBackend{{RunningPosition: 1, NextPosition: 0}},
			removed: []int{0},
		},
		{
			name: "a changed entry is a removed Backend plus an added one",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
    health_check_path: /health
`,
			kept:    []KeptBackend{{RunningPosition: 0, NextPosition: 0}},
			added:   []int{1},
			removed: []int{1},
		},
		{
			name: "reordered entries keep their Backends",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: b:8080
  - url: a:8080
`,
			kept: []KeptBackend{{RunningPosition: 1, NextPosition: 0}, {RunningPosition: 0, NextPosition: 1}},
		},
		{
			name: "an entry spelled differently but normalizing the same is kept",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: http://a:8080/
  - url: b:8080
`,
			kept: []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
		},
		{
			name: "a second copy of a listed address is one more Backend",
			next: `
port: 8000
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
  - url: a:8080
`,
			kept:  []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
			added: []int{2},
		},
		{
			name: "health_checker_time is a Probe setting",
			next: `
port: 8000
health_checker_time: 5s
backends:
  - url: a:8080
  - url: b:8080
`,
			kept:            []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
			probeSettingsUp: true,
		},
		{
			name: "health_checker_timeout is a Probe setting",
			next: `
port: 8000
health_checker_time: 30s
health_checker_timeout: 1s
backends:
  - url: a:8080
  - url: b:8080
`,
			kept:            []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
			probeSettingsUp: true,
		},
		{
			name: "writing out a default the running config left unset is no change",
			next: `
host: 0.0.0.0
port: 8000
type: round-robin
health_checker_time: 30s
monitoring:
  host: localhost
  port: "8001"
backends:
  - url: a:8080
  - url: b:8080
`,
			kept: []KeptBackend{{RunningPosition: 0, NextPosition: 0}, {RunningPosition: 1, NextPosition: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			running := runningConfigYAML
			if tt.running != "" {
				running = tt.running
			}

			plan, err := PlanReload(runningConfig(t, running), []byte(tt.next))

			require.NoError(t, err)
			assert.Equal(t, tt.kept, plan.KeptBackends)
			assert.Equal(t, tt.added, plan.AddedBackendNextPositions)
			assert.Equal(t, tt.removed, plan.RemovedBackendRunningPositions)
			assert.Equal(t, tt.probeSettingsUp, plan.ProbeSettingsChanged)
			require.NotNil(t, plan.NextConfig)
			assert.NotNil(t, plan.NextConfig.HealthCheckerFunc, "the next config comes back prepared")
		})
	}
}

func TestPlanReloadRejectsRestartOnlyKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		running     string
		next        string
		changedKeys []string
	}{
		{
			name: "listener port",
			next: `
port: 9000
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
`,
			changedKeys: []string{"port"},
		},
		{
			name: "balancer type",
			next: `
port: 8000
type: random
health_checker_time: 30s
backends:
  - url: a:8080
  - url: b:8080
`,
			changedKeys: []string{"type"},
		},
		{
			name: "a nested server key is named in full",
			next: `
port: 8000
health_checker_time: 30s
server:
  proxy_timeout: 5s
  http2:
    max_concurrent_streams: 10
backends:
  - url: a:8080
  - url: b:8080
`,
			changedKeys: []string{"server.http2.max_concurrent_streams", "server.proxy_timeout"},
		},
		{
			name: "logging, monitoring, custom headers and middlewares",
			next: `
port: 8000
health_checker_time: 30s
logging:
  level: debug
monitoring:
  port: "9001"
custom_headers:
  X-Client: $remote_addr
middlewares:
  - name: m
    code: x
backends:
  - url: a:8080
  - url: b:8080
`,
			changedKeys: []string{"custom_headers", "logging.level", "middlewares", "monitoring.port"},
		},
		{
			name: "a restart-only key rejects the Backend changes made alongside it",
			next: `
port: 9000
health_checker_time: 5s
backends:
  - url: c:8080
`,
			changedKeys: []string{"port"},
		},
		{
			name: "w-round-robin growing past one Backend changes the running Balancer",
			running: `
port: 8000
type: w-round-robin
backends:
  - url: a:8080
    weight: 1
`,
			next: `
port: 8000
type: w-round-robin
backends:
  - url: a:8080
    weight: 1
  - url: b:8080
    weight: 2
`,
			changedKeys: []string{"type"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			running := runningConfigYAML
			if tt.running != "" {
				running = tt.running
			}

			plan, err := PlanReload(runningConfig(t, running), []byte(tt.next))

			assert.Nil(t, plan)
			var restartRequired *RestartRequiredError
			require.ErrorAs(t, err, &restartRequired)
			assert.Equal(t, tt.changedKeys, restartRequired.ChangedKeys)
			for _, key := range tt.changedKeys {
				assert.Contains(t, err.Error(), key, "the rejection names every offending key")
			}
		})
	}
}

func TestPlanReloadRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		next string
	}{
		{name: "empty file", next: ""},
		{name: "unparseable YAML", next: "port: 8000\nbackends: [\n"},
		{name: "unknown key", next: "port: 8000\nbakends:\n  - url: a:8080\n"},
		{name: "no backends", next: "port: 8000\nbackends: []\n"},
		{name: "invalid backend url", next: "port: 8000\nbackends:\n  - url: https://a:8443\n"},
		{name: "invalid Probe setting", next: "port: 8000\nhealth_checker_timeout: -1s\nbackends:\n  - url: a:8080\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan, err := PlanReload(runningConfig(t, runningConfigYAML), []byte(tt.next))

			assert.Nil(t, plan)
			assert.ErrorIs(t, err, ErrReloadInvalidConfig)
		})
	}
}
