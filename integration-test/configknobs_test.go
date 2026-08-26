package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
)

// A default-config divisor must be reachable from outside its container: the
// client server's host defaults to 0.0.0.0 (a load balancer accepts outside
// traffic). This doubles as the published Docker image's reachability
// guarantee — with a localhost default the mapped port would refuse.
func TestDefaultBindIsReachableFromOutsideContainer(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "bindflip",
		OmitHost: true,
		Backends: []BackendSpec{{ID: "a"}},
	})

	res := s.MustEcho(t, http.MethodGet, "/default-bind", nil, nil)
	if res.Echo.Backend != "a" {
		t.Errorf("request through the default bind reached backend %q, want %q", res.Echo.Backend, "a")
	}
}

// server.read_buffer_size bounds request header size on the HTTP/1.1 path:
// a header over the 4KB library default fails with 431, and raising the knob
// is the (previously unavailable) fix.
func TestOversizedHeadersNeedRaisedReadBufferSize(t *testing.T) {
	t.Parallel()

	bigHeader := http.Header{"X-Big-Cookie": []string{strings.Repeat("j", 8192)}}

	t.Run("DefaultBufferRejects431", func(t *testing.T) {
		t.Parallel()
		s := startScenario(t, ScenarioSpec{
			Name:     "hdrbuf-default",
			Backends: []BackendSpec{{ID: "a"}},
		})
		res, err := s.Request(http.MethodGet, "/big-header", nil, bigHeader)
		if err != nil {
			t.Fatalf("request with an oversized header failed at transport level: %v", err)
		}
		if res.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("oversized header at the default buffer got status %d, want %d", res.StatusCode, http.StatusRequestHeaderFieldsTooLarge)
		}
	})

	t.Run("RaisedBufferAccepts", func(t *testing.T) {
		t.Parallel()
		s := startScenario(t, ScenarioSpec{
			Name:           "hdrbuf-raised",
			ReadBufferSize: 32768,
			Backends:       []BackendSpec{{ID: "a"}},
		})
		res := s.MustEcho(t, http.MethodGet, "/big-header", nil, bigHeader)
		if got := len(res.Echo.Header("X-Big-Cookie")); got != 8192 {
			t.Errorf("backend saw an X-Big-Cookie of %d bytes, want 8192", got)
		}
	})
}

// server.tls_min_version: "1.3" must refuse a TLS 1.2 handshake while a 1.3
// client connects normally.
func TestTLSMinVersion13RefusesTLS12(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:          "tlsmin",
		TLS:           true,
		TLSMinVersion: "1.3",
		Backends:      []BackendSpec{{ID: "a"}},
	})

	cp := x509.NewCertPool()
	cp.AppendCertsFromPEM(s.Certs.CAPEM)
	addr := strings.TrimPrefix(s.BaseURL, "https://")

	t.Run("TLS12HandshakeRefused", func(t *testing.T) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{
			RootCAs:    cp,
			ServerName: "localhost",
			MaxVersion: tls.VersionTLS12,
		})
		if err == nil {
			conn.Close()
			t.Fatalf("a TLS 1.2 handshake succeeded although tls_min_version is 1.3")
		}
	})

	t.Run("TLS13HandshakeAccepted", func(t *testing.T) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{
			RootCAs:    cp,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS13,
		})
		if err != nil {
			t.Fatalf("a TLS 1.3 handshake failed: %v", err)
		}
		conn.Close()
	})
}

// divisor --version prints version and short commit and exits 0; a
// non-release build shows the "dev" placeholders.
func TestVersionFlagPrintsAndExitsZero(t *testing.T) {
	t.Parallel()

	name := namePrefix + "versionflag"
	if err := removeContainerExact(name); err != nil {
		t.Fatalf("removing stale container %s: %v", name, err)
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Name:       name,
		Repository: divisorImage,
		Tag:        imageTag,
		Cmd:        []string{"--version"},
	}, publishPorts)
	if err != nil {
		t.Fatalf("starting divisor container: %v", err)
	}
	t.Cleanup(func() { pool.Purge(res) })

	if code := waitContainerExit(t, res, 60*time.Second); code != 0 {
		t.Errorf("divisor --version exited %d, want 0", code)
	}

	var buf bytes.Buffer
	if err := pool.Client.Logs(dc.LogsOptions{
		Container:    res.Container.ID,
		OutputStream: &buf,
		ErrorStream:  &buf,
		Stdout:       true,
		Stderr:       true,
		Tail:         "10",
	}); err != nil {
		t.Fatalf("fetching container output: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"divisor version dev", "build dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("--version output %q does not contain %q", out, want)
		}
	}
}
