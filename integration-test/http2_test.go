package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
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

	t.Run("BodyOverLimit413", func(t *testing.T) {
		// nethttp_adapter rejects the declared Content-Length up front,
		// so unlike the fasthttp path the 413 arrives cleanly.
		body := testBody(5 << 20)
		res, err := s.Request(http.MethodPost, "/h2toolarge", bytes.NewReader(body), nil)
		if err != nil {
			t.Fatalf("5MB POST over HTTP/2 failed at transport level: %v", err)
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("5MB POST over HTTP/2 got status %d, want 413", res.StatusCode)
		}
		if res.Echo != nil {
			t.Errorf("5MB POST reached backend %s; the size limit did not apply on the HTTP/2 path", res.Echo.Backend)
		}
	})

	t.Run("ChunkedRequest", func(t *testing.T) {
		// The adapter buffers length-less bodies up to the cap; the
		// payload must still arrive at the backend byte-intact.
		body := testBody(128 << 10)
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/h2chunked", rd, nil)
		if err != nil {
			t.Fatalf("length-less POST failed: %v", err)
		}
		if res.StatusCode != http.StatusOK || res.Echo == nil {
			t.Fatalf("length-less POST: status %d, body %.200s", res.StatusCode, res.Body)
		}
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("length-less body arrived corrupted at backend")
		}
	})

	t.Run("ChunkedBodyOverLimit413", func(t *testing.T) {
		// No Content-Length forces the adapter's buffered cap instead of
		// the up-front Content-Length reject.
		body := testBody(5 << 20)
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/h2chunkedtoolarge", rd, nil)
		if err != nil {
			t.Logf("oversized length-less POST failed at transport level instead of a clean 413: %v", err)
			return
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized length-less POST over HTTP/2 got status %d, want 413", res.StatusCode)
		}
		if res.Echo != nil {
			t.Errorf("oversized length-less POST reached backend %s", res.Echo.Backend)
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
	clients := startClientContainers(t, s, 6)
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

const bodyRewriterMW = `
package mw

import (
	"strconv"
	"strings"

	"github.com/aaydin-tr/divisor/middleware"
)

type Rewriter struct{}

func (m *Rewriter) OnRequest(ctx *middleware.Context) error { return nil }

func (m *Rewriter) OnResponse(ctx *middleware.Context, err error) error {
	if err != nil {
		return err
	}
	n, convErr := strconv.Atoi(string(ctx.Request.Header.Peek("X-Rewrite-Len")))
	if convErr != nil || n <= 0 {
		return nil
	}
	ctx.Response.SetBodyString(strings.Repeat("R", n))
	return nil
}

func New(config map[string]any) middleware.Middleware { return &Rewriter{} }
`

// A middleware body rewrite leaves the backend's parsed Content-Length
// stale; the adapter must not forward it, or net/http truncates longer
// rewrites and breaks the stream on shorter ones. Exercised end to end over
// real HTTP/2 because only the net/http path had the bug.
func TestHTTP2MiddlewareBodyRewrite(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:  "h2rw",
		Type:  "round-robin",
		HTTP2: true,
		Backends: []BackendSpec{
			{ID: "a"},
		},
		Middlewares: []MiddlewareSpec{
			{Name: "rewriter", Code: bodyRewriterMW},
		},
	})

	// The echo reply is a few hundred bytes, so 16 is shorter than the stale
	// Content-Length and 65536 is far longer.
	for _, n := range []int{16, 65536} {
		res, err := s.Request(http.MethodGet, fmt.Sprintf("/rewrite?n=%d", n), nil,
			http.Header{"X-Rewrite-Len": []string{fmt.Sprintf("%d", n)}})
		if err != nil {
			t.Fatalf("rewrite to %d bytes: %v", n, err)
		}
		if res.ProtoMajor != 2 {
			t.Fatalf("negotiated %s, want HTTP/2", res.Proto)
		}
		if len(res.Body) != n {
			t.Fatalf("rewrite to %d bytes: client received %d bytes", n, len(res.Body))
		}
		if trimmed := strings.TrimLeft(string(res.Body), "R"); trimmed != "" {
			t.Errorf("rewrite to %d bytes: body corrupted, unexpected suffix %.50q", n, trimmed)
		}
	}
}
