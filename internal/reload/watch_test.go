package reload

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const quietPeriodForTests = 100 * time.Millisecond

const fourBackendsYAML = "port: 8000\nbackends:\n  - url: a:8080\n  - url: b:8080\n  - url: c:8080\n  - url: d:8080\n"

// planReceivingTarget hands every applied Reload to the test goroutine.
type planReceivingTarget chan *config.ReloadPlan

func (target planReceivingTarget) ApplyReload(plan *config.ReloadPlan) { target <- plan }

func receiveAppliedPlan(t *testing.T, target planReceivingTarget) *config.ReloadPlan {
	t.Helper()
	select {
	case plan := <-target:
		return plan
	case <-time.After(5 * time.Second):
		t.Fatal("no Reload applied within 5s")
		return nil
	}
}

func assertNoReloadApplied(t *testing.T, target planReceivingTarget) {
	t.Helper()
	select {
	case plan := <-target:
		t.Fatalf("unexpected Reload applied: %+v", plan)
	case <-time.After(5 * quietPeriodForTests):
	}
}

// watchConfigFileAsMainDoes starts watching before the running config is
// read, then Reloads through a Reloader, as main wires it.
func watchConfigFileAsMainDoes(t *testing.T, configPath string) planReceivingTarget {
	t.Helper()
	watch, err := WatchConfigFile(configPath, quietPeriodForTests)
	require.NoError(t, err)
	return startReloadingUntilTestEnds(t, watch, configPath, prepareRunningConfig(t, configPath))
}

func startReloadingUntilTestEnds(t *testing.T, watch *ConfigFileWatch, configPath string, running *config.Config) planReceivingTarget {
	t.Helper()
	target := make(planReceivingTarget, 10)
	reloader := NewReloader(configPath, running, target)
	watch.StartReloading(reloader)
	t.Cleanup(func() {
		assert.NoError(t, watch.Close())
		reloader.Close()
	})
	return target
}

func TestConfigFileWatchReloadsAnEditedConfigFile(t *testing.T) {
	logs := observeLogs(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, configPath, twoBackendsYAML)
	target := watchConfigFileAsMainDoes(t, configPath)

	writeConfigFile(t, configPath, threeBackendsYAML)

	assert.Equal(t, []int{2}, receiveAppliedPlan(t, target).AddedBackendNextPositions)
	applied := logs.FilterMessage("Reload applied").All()
	require.Len(t, applied, 1)
	assert.Equal(t, "config file change", applied[0].ContextMap()["trigger"])
}

func TestConfigFileWatchReloadsOnceForABurstOfEdits(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, configPath, twoBackendsYAML)
	target := watchConfigFileAsMainDoes(t, configPath)

	writeConfigFile(t, configPath, fourBackendsYAML)
	writeConfigFile(t, configPath, twoBackendsYAML)
	writeConfigFile(t, configPath, threeBackendsYAML)

	assert.Equal(t, []int{2}, receiveAppliedPlan(t, target).AddedBackendNextPositions, "the burst Reloads once, with the last edit")
	assertNoReloadApplied(t, target)
}

// kubelet updates a ConfigMap volume by writing a new data directory and
// renaming a ..data_tmp symlink over ..data; config.yaml is a symlink into
// ..data and is never written itself.
func TestConfigFileWatchReloadsAConfigMapUpdate(t *testing.T) {
	volume := t.TempDir()
	writeConfigMapData(t, volume, "..2026_10_29_10_00_00.1", twoBackendsYAML)
	requireSymlink(t, "..2026_10_29_10_00_00.1", filepath.Join(volume, "..data"))
	requireSymlink(t, filepath.Join("..data", "config.yaml"), filepath.Join(volume, "config.yaml"))
	target := watchConfigFileAsMainDoes(t, filepath.Join(volume, "config.yaml"))

	writeConfigMapData(t, volume, "..2026_10_29_10_05_00.2", threeBackendsYAML)
	requireSymlink(t, "..2026_10_29_10_05_00.2", filepath.Join(volume, "..data_tmp"))
	require.NoError(t, os.Rename(filepath.Join(volume, "..data_tmp"), filepath.Join(volume, "..data")))
	require.NoError(t, os.RemoveAll(filepath.Join(volume, "..2026_10_29_10_00_00.1")))

	assert.Equal(t, []int{2}, receiveAppliedPlan(t, target).AddedBackendNextPositions)
	assertNoReloadApplied(t, target)
}

func writeConfigMapData(t *testing.T, volume, dataDir, configYAML string) {
	t.Helper()
	require.NoError(t, os.Mkdir(filepath.Join(volume, dataDir), 0o755))
	writeConfigFile(t, filepath.Join(volume, dataDir, "config.yaml"), configYAML)
}

func requireSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this system cannot create symlinks: %v", err)
	}
}

func TestConfigFileWatchIgnoresOtherFilesInTheDirectory(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	writeConfigFile(t, configPath, twoBackendsYAML)
	target := watchConfigFileAsMainDoes(t, configPath)

	writeConfigFile(t, filepath.Join(directory, "cert.pem"), "not a config")
	assertNoReloadApplied(t, target)

	writeConfigFile(t, configPath, threeBackendsYAML)
	assert.Equal(t, []int{2}, receiveAppliedPlan(t, target).AddedBackendNextPositions, "the watch is still running")
}

// An edit landing while divisor starts (after the watch began, before the
// Reloader exists) is not lost.
func TestConfigFileWatchReloadsAnEditMadeDuringStartup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, configPath, twoBackendsYAML)
	watch, err := WatchConfigFile(configPath, quietPeriodForTests)
	require.NoError(t, err)
	running := prepareRunningConfig(t, configPath)

	writeConfigFile(t, configPath, threeBackendsYAML)
	target := startReloadingUntilTestEnds(t, watch, configPath, running)

	assert.Equal(t, []int{2}, receiveAppliedPlan(t, target).AddedBackendNextPositions)
}

func TestConfigFileWatchStopsReloadingOnceClosed(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, configPath, twoBackendsYAML)
	watch, err := WatchConfigFile(configPath, quietPeriodForTests)
	require.NoError(t, err)
	target := make(planReceivingTarget, 10)
	watch.StartReloading(NewReloader(configPath, prepareRunningConfig(t, configPath), target))

	require.NoError(t, watch.Close())
	writeConfigFile(t, configPath, threeBackendsYAML)

	assertNoReloadApplied(t, target)
}

func TestWatchConfigFileFailsForAMissingDirectory(t *testing.T) {
	_, err := WatchConfigFile(filepath.Join(t.TempDir(), "missing", "config.yaml"), quietPeriodForTests)

	assert.Error(t, err)
}
