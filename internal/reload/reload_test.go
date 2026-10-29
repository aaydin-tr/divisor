package reload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const twoBackendsYAML = "port: 8000\nbackends:\n  - url: a:8080\n  - url: b:8080\n"
const threeBackendsYAML = "port: 8000\nbackends:\n  - url: a:8080\n  - url: b:8080\n  - url: c:8080\n"

type recordingTarget struct {
	appliedPlans []*config.ReloadPlan
}

func (r *recordingTarget) ApplyReload(plan *config.ReloadPlan) {
	r.appliedPlans = append(r.appliedPlans, plan)
}

func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, observed := observer.New(zapcore.InfoLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))
	return observed
}

func writeConfigFile(t *testing.T, path, configYAML string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))
}

// prepareRunningConfig reads the config divisor boots on, as main does.
func prepareRunningConfig(t *testing.T, configPath string) *config.Config {
	t.Helper()
	running, err := config.ParseConfigFile(configPath)
	require.NoError(t, err)
	require.NoError(t, running.PrepareConfig())
	return running
}

func startReloader(t *testing.T, runningYAML string) (*Reloader, *recordingTarget, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, path, runningYAML)
	target := &recordingTarget{}
	return NewReloader(path, prepareRunningConfig(t, path), target), target, path
}

func TestReloadAppliesTheEditedBackendSet(t *testing.T) {
	logs := observeLogs(t)
	reloader, target, path := startReloader(t, twoBackendsYAML)

	writeConfigFile(t, path, threeBackendsYAML)
	require.NoError(t, reloader.Reload(TriggerSIGHUP))

	require.Len(t, target.appliedPlans, 1)
	assert.Equal(t, []int{2}, target.appliedPlans[0].AddedBackendNextPositions)
	applied := logs.FilterMessage("Reload applied").All()
	require.Len(t, applied, 1)
	assert.Equal(t, "SIGHUP", applied[0].ContextMap()["trigger"])
	assert.Equal(t, []any{"c:8080"}, applied[0].ContextMap()["added_backends"])
	assert.Equal(t, []any{}, applied[0].ContextMap()["removed_backends"])
}

func TestReloadComparesAgainstTheLastAppliedConfig(t *testing.T) {
	logs := observeLogs(t)
	reloader, target, path := startReloader(t, twoBackendsYAML)
	writeConfigFile(t, path, threeBackendsYAML)
	require.NoError(t, reloader.Reload(TriggerSIGHUP))

	writeConfigFile(t, path, twoBackendsYAML)
	require.NoError(t, reloader.Reload(TriggerSIGHUP))

	require.Len(t, target.appliedPlans, 2)
	assert.Empty(t, target.appliedPlans[1].AddedBackendNextPositions)
	assert.Equal(t, []int{2}, target.appliedPlans[1].RemovedBackendRunningPositions, "c was added by the first Reload, so the second removes it")
	assert.Equal(t, []any{"c:8080"}, logs.FilterMessage("Reload applied").All()[1].ContextMap()["removed_backends"])
}

func TestReloadRejectsARestartOnlyKeyAndKeepsTheRunningConfig(t *testing.T) {
	logs := observeLogs(t)
	reloader, target, path := startReloader(t, twoBackendsYAML)

	writeConfigFile(t, path, "port: 9000\nbackends:\n  - url: a:8080\n  - url: b:8080\n  - url: c:8080\n")
	err := reloader.Reload(TriggerSIGHUP)

	var restartRequired *config.RestartRequiredError
	require.ErrorAs(t, err, &restartRequired)
	assert.Empty(t, target.appliedPlans, "a rejected Reload changes nothing")
	rejected := logs.FilterMessage("Reload rejected, divisor keeps serving the running config").All()
	require.Len(t, rejected, 1)
	assert.Equal(t, zapcore.ErrorLevel, rejected[0].Level)
	assert.Contains(t, rejected[0].ContextMap()["error"], "port", "the log names the offending key")

	writeConfigFile(t, path, threeBackendsYAML)
	require.NoError(t, reloader.Reload(TriggerSIGHUP), "the running config is still the one booted")
	assert.Equal(t, []int{2}, target.appliedPlans[0].AddedBackendNextPositions)
}

func TestReloadRejectsAnInvalidOrUnreadableConfig(t *testing.T) {
	tests := []struct {
		name        string
		replaceFile func(t *testing.T, path string)
	}{
		{"invalid", func(t *testing.T, path string) { writeConfigFile(t, path, "port: 8000\nbackends: []\n") }},
		{"unparseable", func(t *testing.T, path string) { writeConfigFile(t, path, "port: [\n") }},
		{"missing", func(t *testing.T, path string) { require.NoError(t, os.Remove(path)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := observeLogs(t)
			reloader, target, path := startReloader(t, twoBackendsYAML)

			tt.replaceFile(t, path)

			assert.Error(t, reloader.Reload(TriggerSIGHUP))
			assert.Empty(t, target.appliedPlans)
			assert.Equal(t, 1, logs.FilterMessage("Reload rejected, divisor keeps serving the running config").Len())
		})
	}
}

func TestReloadAfterCloseIsRefused(t *testing.T) {
	reloader, target, path := startReloader(t, twoBackendsYAML)
	writeConfigFile(t, path, threeBackendsYAML)

	reloader.Close()

	assert.ErrorIs(t, reloader.Reload(TriggerSIGHUP), ErrReloaderClosed)
	assert.Empty(t, target.appliedPlans)
}
