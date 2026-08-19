package integration

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
)

// EchoResp is the JSON the Echo backend returns; it is the suite's main
// assertion channel ("which Backend served this, and what did it see?").
// Keep field-for-field in sync with echoResponse in echobackend/main.go —
// the two live in separate modules and json.Unmarshal drops unknown keys
// silently, so drift shows up only as zero-valued assertions.
type EchoResp struct {
	Backend       string              `json:"backend"`
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	RawQuery      string              `json:"raw_query"`
	Proto         string              `json:"proto"`
	RemoteAddr    string              `json:"remote_addr"`
	ContentLength int64               `json:"content_length"`
	BodyLen       int                 `json:"body_len"`
	BodySha256    string              `json:"body_sha256"`
	BodyB64       string              `json:"body_b64"`
	Headers       map[string][]string `json:"headers"`
}

func (e *EchoResp) Body(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(e.BodyB64)
	if err != nil {
		t.Fatalf("decoding echoed body: %v", err)
	}
	return b
}

func (e *EchoResp) Header(name string) string {
	vals := e.Headers[http.CanonicalHeaderKey(name)]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

type Result struct {
	StatusCode int
	Proto      string
	ProtoMajor int
	Header     http.Header
	Body       []byte
	TLS        *tls.ConnectionState
	Echo       *EchoResp // non-nil when the response body parsed as an Echo backend reply
}

// NewClient builds an HTTP client wired for this Scenario's scheme: the
// suite CA for TLS, and h2 ALPN only for HTTP/2 Scenarios.
func (s *Scenario) NewClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DisableCompression:  true,
		MaxIdleConnsPerHost: 10,
	}
	if s.useTLS {
		cp := x509.NewCertPool()
		cp.AppendCertsFromPEM(s.Certs.CAPEM)
		tr.TLSClientConfig = &tls.Config{RootCAs: cp}
		tr.ForceAttemptHTTP2 = s.Spec.HTTP2
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s *Scenario) Request(method, path string, body io.Reader, hdr http.Header) (*Result, error) {
	req, err := http.NewRequest(method, s.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	for k, vals := range hdr {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	res := &Result{
		StatusCode: resp.StatusCode,
		Proto:      resp.Proto,
		ProtoMajor: resp.ProtoMajor,
		Header:     resp.Header,
		Body:       raw,
		TLS:        resp.TLS,
	}
	if resp.Header.Get("X-Backend-Id") != "" {
		var echo EchoResp
		if json.Unmarshal(raw, &echo) == nil && echo.Backend != "" {
			res.Echo = &echo
		}
	}
	return res, nil
}

// MustEcho fails the test unless the request reached an Echo backend and
// came back 200.
func (s *Scenario) MustEcho(t *testing.T, method, path string, body []byte, hdr http.Header) *Result {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	res, err := s.Request(method, path, rd, hdr)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: status %d, body %.300s", method, path, res.StatusCode, res.Body)
	}
	if res.Echo == nil {
		t.Fatalf("%s %s: response is not an echo reply: %.300s", method, path, res.Body)
	}
	return res
}

func (s *Scenario) Get(t *testing.T, path string) *Result {
	t.Helper()
	return s.MustEcho(t, http.MethodGet, path, nil, nil)
}

// BackendOf reports which Backend served a GET to path.
func (s *Scenario) BackendOf(t *testing.T, path string) string {
	t.Helper()
	return s.Get(t, path).Echo.Backend
}

// InternalURL is how containers on the suite network reach divisor.
func (s *Scenario) InternalURL() string {
	scheme := "http"
	if s.useTLS {
		scheme = "https"
	}
	return scheme + "://" + s.DivisorName + ":" + containerPort
}

func (s *Scenario) KillBackend(t *testing.T, id string) {
	t.Helper()
	e := s.Backend(id)
	if e == nil {
		t.Fatalf("no backend %q", id)
	}
	if err := pool.Client.KillContainer(dc.KillContainerOptions{ID: e.Resource.Container.ID}); err != nil {
		t.Fatalf("killing backend %s: %v", id, err)
	}
	// KillContainer returns on signal dispatch, not process death; wait so
	// tests can assert on the death immediately.
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := pool.Client.InspectContainer(e.Resource.Container.ID) //nolint:staticcheck
		if err != nil || !c.State.Running {
			return // inspect error means the container is already gone
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend %s still running 10s after docker kill", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Scenario) PauseBackend(t *testing.T, id string) {
	t.Helper()
	e := s.Backend(id)
	if e == nil {
		t.Fatalf("no backend %q", id)
	}
	if err := pool.Client.PauseContainer(e.Resource.Container.ID); err != nil {
		t.Fatalf("pausing backend %s: %v", id, err)
	}
}

func (s *Scenario) UnpauseBackend(t *testing.T, id string) {
	t.Helper()
	e := s.Backend(id)
	if e == nil {
		t.Fatalf("no backend %q", id)
	}
	if err := pool.Client.UnpauseContainer(e.Resource.Container.ID); err != nil {
		t.Fatalf("unpausing backend %s: %v", id, err)
	}
}

// TerminateDivisor sends SIGTERM (what an orchestrator sends on shutdown).
func (s *Scenario) TerminateDivisor(t *testing.T) {
	t.Helper()
	err := pool.Client.KillContainer(dc.KillContainerOptions{
		ID:     s.Divisor.Container.ID,
		Signal: dc.SIGTERM,
	})
	if err != nil {
		t.Fatalf("sending SIGTERM to divisor: %v", err)
	}
}

// WaitDivisorExit blocks until the divisor container exits and returns its
// exit code.
func (s *Scenario) WaitDivisorExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	return waitContainerExit(t, s.Divisor, timeout)
}

// ClientContainer is a curl-equipped container on the suite network. Each
// one has its own IP, which is the only way to give ip-hash distinct
// client addresses (host-originated requests all share one source IP).
type ClientContainer struct {
	Name     string
	Resource *dockertest.Resource
}

const curlImage = "curlimages/curl"
const curlTag = "8.11.1"

func startClientContainers(t *testing.T, scenarioName string, n int) []*ClientContainer {
	t.Helper()
	clients := make([]*ClientContainer, 0, n)
	for i := 0; i < n; i++ {
		name := namePrefix + scenarioName + "-client-" + strconv.Itoa(i)
		if err := removeContainerExact(name); err != nil {
			t.Fatalf("removing stale container %s: %v", name, err)
		}
		res, err := pool.RunWithOptions(&dockertest.RunOptions{
			Name:       name,
			Repository: curlImage,
			Tag:        curlTag,
			Entrypoint: []string{"sleep"},
			Cmd:        []string{"infinity"},
			Networks:   []*dockertest.Network{network},
		})
		if err != nil {
			t.Fatalf("starting client container %s: %v", name, err)
		}
		t.Cleanup(func() { pool.Purge(res) })
		clients = append(clients, &ClientContainer{Name: name, Resource: res})
	}
	return clients
}

// curlEchoErr runs curl inside the client container against url and parses
// the Echo backend reply. Extra args (e.g. "--http2", "-k") go before the
// URL. The error return makes it usable inside eventually() retry loops.
func (c *ClientContainer) curlEchoErr(url string, extraArgs ...string) (*EchoResp, error) {
	cmd := append([]string{"curl", "-s", "-m", "20"}, extraArgs...)
	cmd = append(cmd, url)
	var out, errOut bytes.Buffer
	code, err := c.Resource.Exec(cmd, dockertest.ExecOptions{StdOut: &out, StdErr: &errOut})
	if err != nil {
		return nil, fmt.Errorf("exec curl: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("curl exited %d: %s", code, errOut.String())
	}
	var echo EchoResp
	if err := json.Unmarshal(out.Bytes(), &echo); err != nil || echo.Backend == "" {
		return nil, fmt.Errorf("response is not an echo reply: %.300s", out.String())
	}
	return &echo, nil
}

// CurlEcho is the fatal-on-error form of curlEchoErr, for call sites outside
// retry loops.
func (c *ClientContainer) CurlEcho(t *testing.T, url string, extraArgs ...string) *EchoResp {
	t.Helper()
	echo, err := c.curlEchoErr(url, extraArgs...)
	if err != nil {
		t.Fatalf("client %s: %v", c.Name, err)
	}
	return echo
}

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}
