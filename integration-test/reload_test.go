package integration

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
)

// configMapPublishScript writes the config YAML ($2) into a new data directory ($1) and swaps it in like kubelet's
// AtomicWriter: a ..data_tmp symlink renamed over ..data, never writing config.yaml itself.
const configMapPublishScript = `set -e
cd /etc/divisor
previous=$(readlink ..data || true)
mkdir "$1"
printf '%s' "$2" > "$1/config.yaml"
ln -s "$1" ..data_tmp
mv -T ..data_tmp ..data
[ -L config.yaml ] || ln -s ..data/config.yaml config.yaml
[ -z "$previous" ] || rm -rf "$previous"`

// publishConfigMapUpdate delivers the Scenario's current rendering to a
// ConfigMapVolume divisor the way kubelet delivers an edited ConfigMap.
func (s *Scenario) publishConfigMapUpdate(t *testing.T) {
	t.Helper()
	cfgYAML, _ := renderConfig(t, s)
	dataDir := time.Now().UTC().Format("..2006_01_02_15_04_05.000000000")
	s.runInDivisor(t, "publishing the ConfigMap update", configMapPublishScript, dataDir, cfgYAML)
}

// rewriteDivisorConfig replaces the config file divisor booted on with the
// Scenario's current rendering, inside the running container.
func (s *Scenario) rewriteDivisorConfig(t *testing.T) {
	t.Helper()
	cfgYAML, _ := renderConfig(t, s)
	s.runInDivisor(t, "rewriting divisor's config file", `printf '%s' "$1" > /etc/divisor/config.yaml`, cfgYAML)
}

// runInDivisor runs a shell script inside divisor's container, with args as $1, $2, ...
func (s *Scenario) runInDivisor(t *testing.T, purpose, script string, args ...string) {
	t.Helper()
	var stderr bytes.Buffer
	code, err := s.Divisor.Exec(
		append([]string{"/bin/sh", "-c", script, "sh"}, args...),
		dockertest.ExecOptions{StdErr: &stderr},
	)
	if err != nil || code != 0 {
		t.Fatalf("%s: exit %d, %v: %s", purpose, code, err, stderr.String())
	}
}

func (s *Scenario) ReloadDivisor(t *testing.T) {
	t.Helper()
	err := pool.Client.KillContainer(dc.KillContainerOptions{ID: s.Divisor.Container.ID, Signal: dc.SIGHUP})
	if err != nil {
		t.Fatalf("sending SIGHUP to divisor: %v", err)
	}
}

func (s *Scenario) addBackend(t *testing.T, b BackendSpec) {
	t.Helper()
	s.Backends = append(s.Backends, startEcho(t, s.Spec.Name, b, s.Network))
	s.Spec.Backends = append(s.Spec.Backends, b)
}

func (s *Scenario) removeBackendFromConfig(id string) {
	for i, e := range s.Backends {
		if e.ID == id {
			s.Backends = append(s.Backends[:i:i], s.Backends[i+1:]...)
			s.Spec.Backends = append(s.Spec.Backends[:i:i], s.Spec.Backends[i+1:]...)
			return
		}
	}
}

// eventuallyLogged waits for a divisor stderr line containing every fragment.
func eventuallyLogged(t *testing.T, s *Scenario, fragments ...string) {
	t.Helper()
	eventually(t, 15*time.Second, "divisor logs a line with "+strings.Join(fragments, " and "), func() error {
		_, stderr := divisorStreams(t, s.Divisor)
		for _, line := range nonBlankLines(stderr) {
			if containsAll(line, fragments) {
				return nil
			}
		}
		return fmt.Errorf("no such line in the logs yet")
	})
}

func containsAll(line string, fragments []string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(line, fragment) {
			return false
		}
	}
	return true
}

func eventuallyServedByEveryBackend(t *testing.T, s *Scenario, path string, backendIDs ...string) {
	t.Helper()
	eventually(t, 15*time.Second, "traffic reaches "+strings.Join(backendIDs, " and "), func() error {
		counts, err := pollBackends(s, 10, path)
		if err != nil {
			return err
		}
		for _, id := range backendIDs {
			if counts[id] == 0 {
				return fmt.Errorf("want traffic on %v, got %v", backendIDs, counts)
			}
		}
		return nil
	})
}

// SIGHUP applies an edited Backend set without a restart and rejects a restart-only key, naming it. The file watch
// sees the same edits, so only the logged SIGHUP Reload proves the signal works.
func TestReloadViaSIGHUP(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "reload-sighup",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
	})
	startedAt := divisorStartedAt(t, s)

	s.addBackend(t, BackendSpec{ID: "b"})
	s.rewriteDivisorConfig(t)
	s.ReloadDivisor(t)

	eventuallyLogged(t, s, `"msg":"Reload applied"`, `"trigger":"SIGHUP"`)
	eventuallyServedByEveryBackend(t, s, "/reload-added", "a", "b")

	s.removeBackendFromConfig("a")
	s.Spec.ProxyTimeout = 5 * time.Second
	s.rewriteDivisorConfig(t)
	s.ReloadDivisor(t)

	eventuallyLogged(t, s, "Reload rejected", `"trigger":"SIGHUP"`, "server.proxy_timeout")
	assertRunningBackendSetKept(t, s, "a")
	assertNotRestarted(t, s, startedAt)
}

// A ConfigMap edit delivered the way kubelet does it (config.yaml itself is never written) Reloads divisor on its
// own: no SIGHUP, no restart.
func TestReloadOnConfigMapUpdate(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:            "reload-configmap",
		Type:            "round-robin",
		Backends:        []BackendSpec{{ID: "a"}},
		ConfigMapVolume: true,
	})
	startedAt := divisorStartedAt(t, s)

	s.addBackend(t, BackendSpec{ID: "b"})
	s.publishConfigMapUpdate(t)

	eventuallyServedByEveryBackend(t, s, "/configmap-added", "a", "b")
	eventuallyLogged(t, s, `"msg":"Reload applied"`, `"trigger":"config file change"`, s.Backend("b").Name)

	s.removeBackendFromConfig("a")
	s.Spec.ProxyTimeout = 5 * time.Second
	s.publishConfigMapUpdate(t)

	eventuallyLogged(t, s, "Reload rejected", `"trigger":"config file change"`, "server.proxy_timeout")
	assertRunningBackendSetKept(t, s, "a")
	assertNotRestarted(t, s, startedAt)
}

// After a rejected Reload, the Backend the rejected edit removed still serves.
func assertRunningBackendSetKept(t *testing.T, s *Scenario, removedBackendID string) {
	t.Helper()
	counts, err := pollBackends(s, 10, "/reload-rejected")
	if err != nil {
		t.Fatalf("divisor stopped serving after a rejected Reload: %v", err)
	}
	if counts[removedBackendID] == 0 {
		t.Errorf("a rejected Reload must keep the running Backend set, but %s got no traffic: %v", removedBackendID, counts)
	}
}

func assertNotRestarted(t *testing.T, s *Scenario, startedAt time.Time) {
	t.Helper()
	if restartedAt := divisorStartedAt(t, s); !restartedAt.Equal(startedAt) {
		t.Errorf("divisor restarted across the Reloads (started %v, booted %v)", restartedAt, startedAt)
	}
}

func divisorStartedAt(t *testing.T, s *Scenario) time.Time {
	t.Helper()
	container, err := pool.Client.InspectContainer(s.Divisor.Container.ID)
	if err != nil {
		t.Fatalf("inspecting divisor: %v", err)
	}
	if !container.State.Running {
		t.Fatalf("divisor is not running: %s", container.State.String())
	}
	return container.State.StartedAt
}
