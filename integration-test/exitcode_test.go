package integration

import (
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
)

// waitContainerExit blocks until the container exits and returns its exit
// code. Scenario.WaitDivisorExit delegates here; boot-failure tests use it
// directly since their containers never become a full Scenario.
func waitContainerExit(t *testing.T, res *dockertest.Resource, timeout time.Duration) int {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		code, err := pool.Client.WaitContainer(res.Container.ID)
		ch <- result{code, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("waiting for container exit: %v", r.err)
		}
		return r.code
	case <-time.After(timeout):
		t.Fatalf("container did not exit within %s", timeout)
		return -1
	}
}

// runDivisorExpectExit starts a divisor container that is expected to exit on
// its own (no readiness wait) and returns its exit code.
func runDivisorExpectExit(t *testing.T, name string, env, entrypoint, cmd []string) int {
	t.Helper()
	if err := removeContainerExact(name); err != nil {
		t.Fatalf("removing stale container %s: %v", name, err)
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Name:       name,
		Repository: divisorImage,
		Tag:        imageTag,
		Env:        env,
		Entrypoint: entrypoint,
		Cmd:        cmd,
	}, publishPorts)
	if err != nil {
		t.Fatalf("starting divisor container: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			dumpLogs(t, name, res)
		}
		pool.Purge(res)
	})
	return waitContainerExit(t, res, 60*time.Second)
}

// A config divisor cannot start from must exit non-zero, so orchestrators
// (compose restart policies, k8s CrashLoopBackOff) notice the failure instead
// of seeing a clean exit 0.
func TestConfigErrorExitsNonZero(t *testing.T) {
	t.Parallel()

	// The image's own entrypoint, pointed at a config file that does not exist.
	t.Run("MissingConfigFile", func(t *testing.T) {
		code := runDivisorExpectExit(t, namePrefix+"exit-missing", nil,
			nil, []string{"--config", "/etc/divisor/does-not-exist.yaml"})
		if code == 0 {
			t.Errorf("divisor exited 0 although its config file does not exist; want non-zero")
		}
	})

	// shWriteConfigAndExec mirrors startScript: write $DIVISOR_CONFIG, then exec divisor
	// so the container's exit code is divisor's.
	const shWriteConfigAndExec = `printf '%s' "$DIVISOR_CONFIG" > /etc/divisor/config.yaml && ` +
		`exec /divisor --config /etc/divisor/config.yaml`

	t.Run("MalformedYAML", func(t *testing.T) {
		code := runDivisorExpectExit(t, namePrefix+"exit-yaml",
			[]string{"DIVISOR_CONFIG={{{ this is not yaml"},
			[]string{"/bin/sh", "-c"}, []string{shWriteConfigAndExec})
		if code == 0 {
			t.Errorf("divisor exited 0 on a config file that is not valid YAML; want non-zero")
		}
	})

	t.Run("NoBackends", func(t *testing.T) {
		// Parses fine but fails validation (PrepareConfig: at least one
		// backend must be set).
		code := runDivisorExpectExit(t, namePrefix+"exit-nobackends",
			[]string{"DIVISOR_CONFIG=host: 0.0.0.0\nport: 8080\nbackends: []\n"},
			[]string{"/bin/sh", "-c"}, []string{shWriteConfigAndExec})
		if code == 0 {
			t.Errorf("divisor exited 0 on a config with no backends; want non-zero")
		}
	})

	t.Run("MalformedProxyTimeout", func(t *testing.T) {
		code := runDivisorExpectExit(t, namePrefix+"exit-proxytimeout",
			[]string{"DIVISOR_CONFIG=host: 0.0.0.0\nport: 8080\nbackends:\n  - url: localhost:9000\nserver:\n  proxy_timeout: banana\n"},
			[]string{"/bin/sh", "-c"}, []string{shWriteConfigAndExec})
		if code == 0 {
			t.Errorf("divisor exited 0 on a non-duration proxy_timeout; want non-zero")
		}
	})

	t.Run("UnloadableCertificate", func(t *testing.T) {
		// cert_file/key_file exist but are not PEM: config validation parses
		// the pair, so divisor exits instead of living on with no listener.
		const shWriteCertsAndExec = `printf 'not a certificate' > /etc/divisor/cert.pem && ` +
			`printf 'not a key' > /etc/divisor/key.pem && ` + shWriteConfigAndExec
		code := runDivisorExpectExit(t, namePrefix+"exit-badcert",
			[]string{"DIVISOR_CONFIG=host: 0.0.0.0\nport: 8080\nbackends:\n  - url: localhost:9000\nserver:\n  cert_file: /etc/divisor/cert.pem\n  key_file: /etc/divisor/key.pem\n"},
			[]string{"/bin/sh", "-c"}, []string{shWriteCertsAndExec})
		if code == 0 {
			t.Errorf("divisor exited 0 on an unloadable TLS key pair; want non-zero")
		}
	})

	t.Run("MalformedMaxRequestBodySize", func(t *testing.T) {
		code := runDivisorExpectExit(t, namePrefix+"exit-bodysize",
			[]string{"DIVISOR_CONFIG=host: 0.0.0.0\nport: 8080\nbackends:\n  - url: localhost:9000\nserver:\n  max_request_body_size: big\n"},
			[]string{"/bin/sh", "-c"}, []string{shWriteConfigAndExec})
		if code == 0 {
			t.Errorf("divisor exited 0 on a non-integer max_request_body_size; want non-zero")
		}
	})
}
