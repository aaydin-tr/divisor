package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
	"gopkg.in/yaml.v3"
)

const (
	divisorImage  = "divisor-it"
	echoImage     = "divisor-echo-it"
	imageTag      = "latest"
	networkName   = "divisor-it"
	namePrefix     = "div-it-"
	containerPort  = "8080"
	monitoringPort = "8001"

	certPath = "/etc/divisor/cert.pem"
	keyPath  = "/etc/divisor/key.pem"
	mwPath   = "/etc/divisor/mw.go"
)

// startScript writes env-injected config/cert/key/middleware files and then
// execs divisor so it runs as PID 1 and receives container signals (SIGTERM
// graceful-shutdown tests depend on the exec).
const startScript = `mkdir -p /etc/divisor && ` +
	`printf '%s' "$DIVISOR_CONFIG" > /etc/divisor/config.yaml && ` +
	`{ [ -z "$DIVISOR_CERT" ] || printf '%s' "$DIVISOR_CERT" > ` + certPath + `; } && ` +
	`{ [ -z "$DIVISOR_KEY" ] || printf '%s' "$DIVISOR_KEY" > ` + keyPath + `; } && ` +
	`{ [ -z "$DIVISOR_MW" ] || printf '%s' "$DIVISOR_MW" > ` + mwPath + `; } && ` +
	`exec /divisor --config /etc/divisor/config.yaml`

type BackendSpec struct {
	ID     string
	Weight uint
	// StartDown boots the Echo backend with a failing /health, so divisor
	// sees it Down from the very first Probe.
	StartDown bool
	// ResponseDelay makes this Echo backend intrinsically slow (applies to
	// echo responses, never to /health probes).
	ResponseDelay time.Duration
}

type MiddlewareSpec struct {
	Name     string
	Code     string
	Config   map[string]any
	Disabled bool
	// ViaFile ships Code into the container as a file and references it with
	// the config `file:` key instead of inline `code:`. At most one
	// middleware per Scenario can use it (single env slot).
	ViaFile bool
}

// ScenarioSpec describes one divisor configuration under test
// (Balancer type x HTTP version x TLS), per CONTEXT.md.
type ScenarioSpec struct {
	Name     string // unique, lowercase, DNS-safe; used in container names
	Type     string // balancer type; divisor defaults it to round-robin when empty
	HTTP2    bool   // implies TLS (divisor rejects http2 without certs)
	TLS      bool
	Backends []BackendSpec
	// HealthCheckerTime defaults to 10m: effectively "no probing" so
	// non-failover tests are immune to probe-driven rotation changes.
	HealthCheckerTime time.Duration
	CustomHeaders     map[string]string
	Middlewares       []MiddlewareSpec
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	ProxyTimeout       time.Duration
	MaxRequestBodySize int
	// ReadBufferSize renders server.read_buffer_size, which bounds request
	// header size on the HTTP/1.1 path.
	ReadBufferSize int
	// TLSMinVersion renders server.tls_min_version ("1.2" or "1.3").
	TLSMinVersion string
	// OmitHost drops the config's `host` key so the scenario runs on
	// divisor's default bind address.
	OmitHost bool
	// LoggingFormat and AccessLog render a `logging:` section; both zero
	// means the section is omitted so the scenario runs on divisor's defaults.
	LoggingFormat string
	AccessLog     bool
	// ExposeMonitoring binds the monitoring server on 0.0.0.0 and publishes
	// its port, so tests can probe /healthz and /ready the way a kubelet does.
	ExposeMonitoring bool
}

type Echo struct {
	ID       string
	Name     string // container name == DNS name on the suite network
	Resource *dockertest.Resource
	HostAddr string // host-mapped address for direct control from tests
}

func (e *Echo) url(path string) string { return "http://" + e.HostAddr + path }

// ctlClient talks to Echo backends' control endpoints (health toggle,
// counters, readiness). Bounded so a wedged container fails one test instead
// of hanging the whole suite on a client with no timeout.
var ctlClient = &http.Client{Timeout: 10 * time.Second}

// SetHealth flips the Echo backend's /health endpoint, which is what
// divisor's Probe watches. This is the deterministic way to take a Backend
// Down (and let it Rejoin) without touching the container.
func (e *Echo) SetHealth(t *testing.T, ok bool) {
	t.Helper()
	resp, err := ctlClient.Post(e.url(fmt.Sprintf("/health?ok=%t", ok)), "", nil)
	if err != nil {
		t.Fatalf("toggle health of %s: %v", e.ID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle health of %s: status %d", e.ID, resp.StatusCode)
	}
}

// Counter returns how many attempts the Echo backend has seen for a fail_key.
func (e *Echo) Counter(t *testing.T, key string) uint64 {
	t.Helper()
	resp, err := ctlClient.Get(e.url("/counters?key=" + key))
	if err != nil {
		t.Fatalf("read counter %q of %s: %v", key, e.ID, err)
	}
	defer resp.Body.Close()
	var out struct {
		Count uint64 `json:"count"`
	}
	if err := decodeJSON(resp.Body, &out); err != nil {
		t.Fatalf("decode counter %q of %s: %v", key, e.ID, err)
	}
	return out.Count
}

type Scenario struct {
	Spec        ScenarioSpec
	Divisor     *dockertest.Resource
	DivisorName string // container name == DNS name on the scenario network
	Backends    []*Echo
	BaseURL     string
	// MonitoringAddr is the host-mapped monitoring address; set only when
	// the spec asks for ExposeMonitoring.
	MonitoringAddr string
	Certs          *certBundle
	Network        *dockertest.Network

	useTLS bool
	client *http.Client
}

func (s *Scenario) Backend(id string) *Echo {
	for _, e := range s.Backends {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func startScenario(t *testing.T, spec ScenarioSpec) *Scenario {
	t.Helper()

	if spec.HealthCheckerTime == 0 {
		spec.HealthCheckerTime = 10 * time.Minute
	}
	s := &Scenario{Spec: spec, useTLS: spec.TLS || spec.HTTP2}

	// One network per Scenario: on a shared network, a killed backend's freed
	// IP can be claimed by another scenario's container while divisor's dialer
	// still holds the DNS-cached IP, silently proxying across scenarios.
	network, err := pool.CreateNetwork(networkName + "-" + spec.Name)
	if err != nil {
		t.Fatalf("creating scenario network: %v", err)
	}
	s.Network = network
	t.Cleanup(func() { network.Close() })

	for _, b := range spec.Backends {
		s.Backends = append(s.Backends, startEcho(t, spec.Name, b, network))
	}

	env := []string{}
	if s.useTLS {
		s.Certs = generateCerts(t)
		env = append(env,
			"DIVISOR_CERT="+string(s.Certs.CertPEM),
			"DIVISOR_KEY="+string(s.Certs.KeyPEM),
		)
	}
	cfgYAML, mwFile := renderConfig(t, s)
	env = append(env, "DIVISOR_CONFIG="+cfgYAML)
	if mwFile != "" {
		env = append(env, "DIVISOR_MW="+mwFile)
	}

	// "-lb" distinguishes the divisor container from its backends in
	// `docker ps`; cleanup itself uses exact-name matching (see
	// removeContainerExact) so names may safely share prefixes.
	name := namePrefix + spec.Name + "-lb"
	if err := removeContainerExact(name); err != nil {
		t.Fatalf("removing stale container %s: %v", name, err)
	}
	exposedPorts := []string{containerPort + "/tcp"}
	if spec.ExposeMonitoring {
		exposedPorts = append(exposedPorts, monitoringPort+"/tcp")
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Name:         name,
		Repository:   divisorImage,
		Tag:          imageTag,
		Env:          env,
		Entrypoint:   []string{"/bin/sh", "-c"},
		Cmd:          []string{startScript},
		ExposedPorts: exposedPorts,
		Networks:     []*dockertest.Network{s.Network},
	}, publishPorts)
	if err != nil {
		t.Fatalf("starting divisor container: %v", err)
	}
	s.Divisor = res
	s.DivisorName = name
	t.Cleanup(func() {
		if t.Failed() {
			dumpLogs(t, name, res)
		}
		pool.Purge(res)
	})

	hostPort := res.GetHostPort(containerPort + "/tcp")
	if hostPort == "" {
		t.Fatalf("divisor container has no mapped port")
	}
	scheme := "http"
	if s.useTLS {
		scheme = "https"
	}
	s.BaseURL = scheme + "://" + hostPort
	if spec.ExposeMonitoring {
		s.MonitoringAddr = res.GetHostPort(monitoringPort + "/tcp")
		if s.MonitoringAddr == "" {
			t.Fatalf("divisor container has no mapped monitoring port")
		}
	}
	s.client = s.NewClient(30 * time.Second)

	// A Scenario whose every Backend starts Down is ready once divisor
	// answers 503 (zero Alive Backends is divisor working); otherwise ready
	// means a Backend echoed.
	allBackendsStartDown := true
	for _, b := range spec.Backends {
		if !b.StartDown {
			allBackendsStartDown = false
		}
	}
	if err := pool.Retry(func() error {
		result, err := s.Request(http.MethodGet, "/?ready=1", nil, nil)
		if err != nil {
			return err
		}
		if allBackendsStartDown {
			if result.StatusCode != http.StatusServiceUnavailable {
				return fmt.Errorf("divisor not answering 503 yet: status %d", result.StatusCode)
			}
			return nil
		}
		if result.StatusCode != http.StatusOK || result.Header.Get("X-Backend-Id") == "" {
			return fmt.Errorf("divisor not proxying yet: status %d", result.StatusCode)
		}
		return nil
	}); err != nil {
		dumpLogs(t, name, res)
		t.Fatalf("divisor never became ready: %v", err)
	}

	return s
}

func startEcho(t *testing.T, scenarioName string, b BackendSpec, network *dockertest.Network) *Echo {
	t.Helper()

	name := namePrefix + scenarioName + "-" + strings.ToLower(b.ID)
	if err := removeContainerExact(name); err != nil {
		t.Fatalf("removing stale container %s: %v", name, err)
	}
	env := []string{"BACKEND_ID=" + b.ID}
	if b.StartDown {
		env = append(env, "START_HEALTHY=false")
	}
	if b.ResponseDelay > 0 {
		env = append(env, "RESPONSE_DELAY="+b.ResponseDelay.String())
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Name:         name,
		Repository:   echoImage,
		Tag:          imageTag,
		Env:          env,
		ExposedPorts: []string{containerPort + "/tcp"},
		Networks:     []*dockertest.Network{network},
	}, publishPorts)
	if err != nil {
		t.Fatalf("starting echo backend %s: %v", b.ID, err)
	}
	t.Cleanup(func() { pool.Purge(res) })

	e := &Echo{ID: b.ID, Name: name, Resource: res, HostAddr: res.GetHostPort(containerPort + "/tcp")}
	if e.HostAddr == "" {
		t.Fatalf("echo backend %s has no mapped port", b.ID)
	}
	// Ready means "the HTTP server answers" -- a StartDown backend answers
	// /health with 503, which is still ready.
	if err := pool.Retry(func() error {
		resp, err := ctlClient.Get(e.url("/health"))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}); err != nil {
		t.Fatalf("echo backend %s never became ready: %v", b.ID, err)
	}
	return e
}

// removeContainerExact force-removes a container by exact name. dockertest's
// RemoveContainerByName filters by substring and removes the newest match,
// which would delete a sibling container whose name merely contains this one.
func removeContainerExact(name string) error {
	containers, err := pool.Client.ListContainers(dc.ListContainersOptions{
		All:     true,
		Filters: map[string][]string{"name": {"^/" + name + "$"}},
	})
	if err != nil {
		return err
	}
	for _, c := range containers {
		err := pool.Client.RemoveContainer(dc.RemoveContainerOptions{ID: c.ID, Force: true, RemoveVolumes: true})
		if err != nil {
			return err
		}
	}
	return nil
}

// pollBackends fires n sequential GETs and tallies which Backend served
// each, erroring on the first non-200 or non-echo reply. It is the
// error-returning sibling of countBackends for use inside eventually.
func pollBackends(s *Scenario, n int, path string) (map[string]int, error) {
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		res, err := s.Request(http.MethodGet, fmt.Sprintf("%s?n=%d", path, i), nil, nil)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK || res.Echo == nil {
			return nil, fmt.Errorf("status %d", res.StatusCode)
		}
		counts[res.Echo.Backend]++
	}
	return counts, nil
}

// renderConfig builds the divisor config YAML for a Scenario. It returns the
// YAML plus, when a middleware uses ViaFile, the middleware source to ship
// into the container.
func renderConfig(t *testing.T, s *Scenario) (string, string) {
	t.Helper()

	backends := make([]map[string]any, 0, len(s.Backends))
	for i, e := range s.Backends {
		b := map[string]any{
			"url":               e.Name + ":" + containerPort,
			"health_check_path": "/health",
		}
		if w := s.Spec.Backends[i].Weight; w > 0 {
			b["weight"] = w
		}
		backends = append(backends, b)
	}

	monitoringHost := "127.0.0.1"
	if s.Spec.ExposeMonitoring {
		monitoringHost = "0.0.0.0"
	}

	server := map[string]any{}
	if s.Spec.HTTP2 {
		server["http_version"] = "http2"
	}
	if s.useTLS {
		server["cert_file"] = certPath
		server["key_file"] = keyPath
	}
	if s.Spec.ReadTimeout > 0 {
		server["read_timeout"] = s.Spec.ReadTimeout.String()
	}
	if s.Spec.WriteTimeout > 0 {
		server["write_timeout"] = s.Spec.WriteTimeout.String()
	}
	if s.Spec.ProxyTimeout > 0 {
		server["proxy_timeout"] = s.Spec.ProxyTimeout.String()
	}
	if s.Spec.MaxRequestBodySize > 0 {
		server["max_request_body_size"] = s.Spec.MaxRequestBodySize
	}
	if s.Spec.ReadBufferSize > 0 {
		server["read_buffer_size"] = s.Spec.ReadBufferSize
	}
	if s.Spec.TLSMinVersion != "" {
		server["tls_min_version"] = s.Spec.TLSMinVersion
	}

	cfg := map[string]any{
		"port":                containerPort,
		"type":                s.Spec.Type,
		"health_checker_time": s.Spec.HealthCheckerTime.String(),
		"backends":            backends,
		"server":              server,
		"monitoring":          map[string]any{"host": monitoringHost, "port": monitoringPort},
	}
	// Explicit 0.0.0.0 matches divisor's own default; OmitHost scenarios prove
	// the default itself keeps the container reachable.
	if !s.Spec.OmitHost {
		cfg["host"] = "0.0.0.0"
	}
	if len(s.Spec.CustomHeaders) > 0 {
		cfg["custom_headers"] = s.Spec.CustomHeaders
	}
	if s.Spec.LoggingFormat != "" || s.Spec.AccessLog {
		loggingSection := map[string]any{}
		if s.Spec.LoggingFormat != "" {
			loggingSection["format"] = s.Spec.LoggingFormat
		}
		if s.Spec.AccessLog {
			loggingSection["access_log"] = true
		}
		cfg["logging"] = loggingSection
	}

	mwFile := ""
	if len(s.Spec.Middlewares) > 0 {
		mws := make([]map[string]any, 0, len(s.Spec.Middlewares))
		for _, mw := range s.Spec.Middlewares {
			m := map[string]any{"name": mw.Name}
			if mw.Disabled {
				m["disabled"] = true
			}
			if mw.ViaFile {
				if mwFile != "" {
					t.Fatalf("at most one middleware per scenario can use ViaFile")
				}
				mwFile = mw.Code
				m["file"] = mwPath
			} else {
				// The leading newline of a backtick-literal snippet must go:
				// yaml.v3 emits strings that start with a newline as `|4`
				// blocks with an explicit indentation indicator that its own
				// parser rejects inside nested sequences.
				m["code"] = strings.TrimLeft(mw.Code, "\n")
			}
			if len(mw.Config) > 0 {
				m["config"] = mw.Config
			}
			mws = append(mws, m)
		}
		cfg["middlewares"] = mws
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling scenario config: %v", err)
	}
	return string(out), mwFile
}

func publishPorts(hc *dc.HostConfig) {
	hc.PublishAllPorts = true
	hc.AutoRemove = false
	hc.RestartPolicy = dc.NeverRestart()
}

// buildImage shells out to the docker CLI instead of using the go-dockerclient
// /build API: the API path streams a hand-rolled tar over the daemon socket,
// which proved unreliable (indefinite hang) on Windows named pipes, while the
// CLI uses BuildKit and honors .dockerignore.
func buildImage(name, contextDir, dockerfile string) error {
	abs, err := filepath.Abs(contextDir)
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "build", "-t", name+":"+imageTag, "-f", filepath.Join(abs, dockerfile), abs)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build %s: %w\n%s", name, err, out)
	}
	return nil
}

func removeStaleContainers() {
	containers, err := pool.Client.ListContainers(dc.ListContainersOptions{
		All:     true,
		Filters: map[string][]string{"name": {namePrefix}},
	})
	if err != nil {
		return
	}
	for _, c := range containers {
		pool.Client.RemoveContainer(dc.RemoveContainerOptions{ID: c.ID, Force: true, RemoveVolumes: true})
	}
}

func removeStaleNetworks() {
	nets, err := pool.NetworksByName(networkName)
	if err != nil {
		return
	}
	for i := range nets {
		nets[i].Close()
	}
}

func dumpLogs(t *testing.T, name string, res *dockertest.Resource) {
	t.Helper()
	var buf bytes.Buffer
	err := pool.Client.Logs(dc.LogsOptions{
		Container:    res.Container.ID,
		OutputStream: &buf,
		ErrorStream:  &buf,
		Stdout:       true,
		Stderr:       true,
		Tail:         "300",
	})
	if err != nil {
		t.Logf("could not fetch logs of %s: %v", name, err)
		return
	}
	t.Logf("---- logs of %s ----\n%s\n---- end logs ----", name, buf.String())
}

// eventually polls cond until it returns nil or the deadline passes; the
// last error becomes the failure message. cond always runs at least once,
// and once more after the deadline, so a condition that turns true during
// the final sleep is not reported as a stale failure.
func eventually(t *testing.T, timeout time.Duration, msg string, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := cond()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: condition not met within %s: %v", msg, timeout, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
