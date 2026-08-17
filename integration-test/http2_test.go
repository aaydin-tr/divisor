package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The HTTP/2 path is a different server stack (net/http + x/net/http2
// behind internal/proxy/nethttp_adapter), so these tests target the adapter
// seam rather than re-running the whole HTTP/1.1 matrix.
func TestHTTP2(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:  "h2",
		Type:  "round-robin",
		HTTP2: true,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	t.Run("ALPNNegotiatesH2", func(t *testing.T) {
		res := s.Get(t, "/negotiate")
		if res.ProtoMajor != 2 {
			t.Fatalf("negotiated %s, want HTTP/2", res.Proto)
		}
		if res.TLS == nil || res.TLS.NegotiatedProtocol != "h2" {
			t.Errorf("ALPN did not negotiate h2")
		}
	})

	t.Run("MethodsWithBodies", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			var body []byte
			if method != http.MethodGet {
				body = []byte("h2-payload-" + method)
			}
			res := s.MustEcho(t, method, "/h2methods?m="+method, body, nil)
			if res.Echo.Method != method {
				t.Errorf("backend saw method %s, want %s", res.Echo.Method, method)
			}
			if body != nil && res.Echo.BodySha256 != sha256Hex(body) {
				t.Errorf("%s body arrived corrupted at backend", method)
			}
		}
	})

	t.Run("BackendSeesHTTP11", func(t *testing.T) {
		// The adapter must downgrade: backends always speak HTTP/1.1
		// regardless of the client-side protocol.
		res := s.Get(t, "/downgrade")
		if res.Echo.Proto != "HTTP/1.1" {
			t.Errorf("backend saw protocol %s, want HTTP/1.1", res.Echo.Proto)
		}
	})

	t.Run("ResponseHopByHopStripped", func(t *testing.T) {
		// Connection-specific headers are illegal in HTTP/2 responses; if
		// divisor forwards them, Go's h2 client rejects the response as
		// malformed, so the request erroring out is itself a failure signal.
		res := s.MustEcho(t, http.MethodGet, "/h2resphdr?rh=Keep-Alive:timeout%3D5&rh=X-H2-Stays:1", nil, nil)
		if res.Header.Get("X-H2-Stays") != "1" {
			t.Errorf("end-to-end response header X-H2-Stays was lost")
		}
		if res.Header.Get("Keep-Alive") != "" {
			t.Errorf("hop-by-hop Keep-Alive leaked into an HTTP/2 response")
		}
	})
}

func TestHTTP2Failover(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:              "h2fo",
		Type:              "round-robin",
		HTTP2:             true,
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	s.Backend("b").SetHealth(t, false)
	eventually(t, 15*time.Second, "traffic converged on the surviving backend", func() error {
		counts, err := pollBackends(s, 10, "/h2fo")
		if err != nil {
			return err
		}
		if counts["b"] > 0 {
			return fmt.Errorf("Down backend b still in rotation (counts: %v)", counts)
		}
		return nil
	})
}

func TestIPHashUnderHTTP2(t *testing.T) {
	t.Parallel()
	// ip-hash hashes ctx.RemoteIP(); on the HTTP/2 path that context is
	// synthesized by nethttp_adapter, so this test proves the real client
	// IP survives the conversion. If it does not, every client below maps
	// to the same backend.
	s := startScenario(t, ScenarioSpec{
		Name:  "h2ih",
		Type:  "ip-hash",
		HTTP2: true,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})
	clients := startClientContainers(t, s.Spec.Name, 6)
	url := s.InternalURL() + "/"

	distinct := map[string]bool{}
	for i, c := range clients {
		first := c.CurlEcho(t, url, "-k", "--http2").Backend
		if got := c.CurlEcho(t, url, "-k", "--http2").Backend; got != first {
			t.Fatalf("client %d flapped between %s and %s over HTTP/2", i, first, got)
		}
		distinct[first] = true
	}
	if len(distinct) < 2 {
		t.Errorf("6 distinct client IPs all mapped to one backend over HTTP/2; the client IP is probably lost in nethttp_adapter")
	}
}
