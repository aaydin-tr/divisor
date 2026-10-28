package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// prometheusPollWindow outlasts a few ticks of the monitoring server's 5s
// Prometheus poller, so /metrics assertions wait on it instead of sleeping.
const prometheusPollWindow = 20 * time.Second

// backendStatsRow mirrors one /stats `backends` row.
type backendStatsRow struct {
	Addr          string `json:"addr"`
	TotalReqCount uint64 `json:"total_req_count"`
	IsHostAlive   bool   `json:"is_host_alive"`
	BackendHash   uint32 `json:"backend_hash"`
}

// getMonitoring GETs a monitoring endpoint over the network and reads the
// whole answer; the returned response's body is already closed.
func getMonitoring(s *Scenario, path string) (*http.Response, []byte, error) {
	resp, err := ctlClient.Get("http://" + s.MonitoringAddr + path)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp, body, err
}

func getMonitoringOK(s *Scenario, path string) (*http.Response, []byte, error) {
	resp, body, err := getMonitoring(s, path)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}
	return resp, body, nil
}

func fetchBackendStats(s *Scenario) ([]backendStatsRow, error) {
	_, body, err := getMonitoringOK(s, "/stats")
	if err != nil {
		return nil, err
	}
	var stats struct {
		Backends []backendStatsRow `json:"backends"`
	}
	if err := decodeJSON(bytes.NewReader(body), &stats); err != nil {
		return nil, err
	}
	return stats.Backends, nil
}

// scrapeMetrics reads /metrics as a Prometheus scraper would and returns
// every sample keyed by its series, e.g. `backend_alive{address="x:8080"}`.
func scrapeMetrics(s *Scenario) (map[string]float64, error) {
	resp, body, err := getMonitoringOK(s, "/metrics")
	if err != nil {
		return nil, err
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		return nil, fmt.Errorf("/metrics Content-Type %q, want the Prometheus text exposition (text/plain)", contentType)
	}
	return parsePrometheusText(body)
}

// parsePrometheusText handles the subset promhttp emits: comment lines and
// `series value` samples without timestamps.
func parsePrometheusText(body []byte) (map[string]float64, error) {
	samples := map[string]float64{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := strings.LastIndexByte(line, ' ')
		if separator < 0 {
			return nil, fmt.Errorf("malformed sample line %q", line)
		}
		value, err := strconv.ParseFloat(line[separator+1:], 64)
		if err != nil {
			return nil, fmt.Errorf("malformed sample value in %q: %w", line, err)
		}
		samples[line[:separator]] = value
	}
	return samples, scanner.Err()
}

func backendSeries(metricName string, backend *Echo) string {
	return fmt.Sprintf("%s{address=%q}", metricName, backendAddress(backend))
}

// backendAddress is the Backend address divisor reports: the config url,
// which the harness renders as host:port.
func backendAddress(backend *Echo) string { return backend.Name + ":" + containerPort }

func requireBackendGauge(samples map[string]float64, metricName string, backend *Echo, want float64) error {
	series := backendSeries(metricName, backend)
	got, ok := samples[series]
	if !ok {
		return fmt.Errorf("/metrics has no %s", series)
	}
	if got != want {
		return fmt.Errorf("%s = %v, want %v", series, got, want)
	}
	return nil
}

// requireStatsRowsInConfigOrder checks /stats lists exactly one row per
// configured Backend, in config order.
func requireStatsRowsInConfigOrder(s *Scenario, rows []backendStatsRow) error {
	if len(rows) != len(s.Backends) {
		return fmt.Errorf("/stats has %d backend rows, want %d (one per configured Backend)", len(rows), len(s.Backends))
	}
	for i, backend := range s.Backends {
		if rows[i].Addr != backendAddress(backend) || rows[i].BackendHash != uint32(i) {
			return fmt.Errorf("/stats row %d is %s (hash %d), want %s (hash %d)",
				i, rows[i].Addr, rows[i].BackendHash, backendAddress(backend), i)
		}
	}
	return nil
}

// readStatsRowsInConfigOrder reads /stats and fails the test unless it lists
// one row per configured Backend in config order; during names the moment.
func readStatsRowsInConfigOrder(t *testing.T, s *Scenario, during string) []backendStatsRow {
	t.Helper()
	rows, err := fetchBackendStats(s)
	if err != nil {
		t.Fatalf("reading /stats %s: %v", during, err)
	}
	if err := requireStatsRowsInConfigOrder(s, rows); err != nil {
		t.Fatalf("/stats %s: %v", during, err)
	}
	return rows
}

func TestMonitoringReportsBackendLivenessAcrossRejoin(t *testing.T) {
	t.Parallel()
	// /metrics backend_alive and /stats is_host_alive read 0/false for a
	// Backend that is Down from boot and flip to 1/true once it Rejoins; the
	// still-Down Backend keeps reading Down.
	s := startScenario(t, ScenarioSpec{
		Name:              "monlive",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		ExposeMonitoring:  true,
		Backends: []BackendSpec{
			{ID: "a", StartDown: true},
			{ID: "b", StartDown: true},
		},
	})
	backendA, backendB := s.Backend("a"), s.Backend("b")

	eventually(t, prometheusPollWindow, "/metrics reports both Backends Down after a zero-Alive boot", func() error {
		samples, err := scrapeMetrics(s)
		if err != nil {
			return err
		}
		if err := requireBackendGauge(samples, "backend_alive", backendA, 0); err != nil {
			return err
		}
		return requireBackendGauge(samples, "backend_alive", backendB, 0)
	})

	rows := readStatsRowsInConfigOrder(t, s, "after a zero-Alive boot")
	for i, row := range rows {
		if row.IsHostAlive {
			t.Errorf("/stats row %d (%s) is_host_alive = true after a zero-Alive boot, want false", i, row.Addr)
		}
	}

	backendA.SetHealth(t, true)

	eventually(t, prometheusPollWindow, "/metrics reports the Rejoined Backend Alive and the other still Down", func() error {
		samples, err := scrapeMetrics(s)
		if err != nil {
			return err
		}
		if err := requireBackendGauge(samples, "backend_alive", backendA, 1); err != nil {
			return err
		}
		return requireBackendGauge(samples, "backend_alive", backendB, 0)
	})

	rows = readStatsRowsInConfigOrder(t, s, "after a Rejoin")
	if !rows[0].IsHostAlive {
		t.Errorf("/stats a is_host_alive = false after it Rejoined, want true")
	}
	if rows[1].IsHostAlive {
		t.Errorf("/stats b is_host_alive = true while still Down, want false")
	}
}

func TestMonitoringReportsPerBackendRequestCounts(t *testing.T) {
	t.Parallel()
	// /stats total_req_count and /metrics backend_total_request_count count
	// the requests proxied to each Backend; a Down Backend in the middle of
	// the config keeps its row, its place and a zero count.
	s := startScenario(t, ScenarioSpec{
		Name:             "monreq",
		Type:             "round-robin",
		ExposeMonitoring: true,
		Backends: []BackendSpec{
			{ID: "a"},
			{ID: "b", StartDown: true},
			{ID: "c"},
		},
	})

	// The harness's readiness request was proxied too, so the Alive
	// Backends' counts are measured from this baseline.
	baseline := readStatsRowsInConfigOrder(t, s, "before traffic")

	const requestCount = 10
	served := countBackends(t, s, requestCount, "/counted")
	if served["a"] != requestCount/2 || served["c"] != requestCount/2 {
		t.Fatalf("round-robin over Alive a and c served %v, want %d each", served, requestCount/2)
	}
	wantTotal := map[string]uint64{
		"a": baseline[0].TotalReqCount + requestCount/2,
		"b": 0,
		"c": baseline[2].TotalReqCount + requestCount/2,
	}
	wantAlive := map[string]bool{"a": true, "b": false, "c": true}

	rows := readStatsRowsInConfigOrder(t, s, "after traffic")
	for i, backend := range s.Backends {
		if rows[i].TotalReqCount != wantTotal[backend.ID] {
			t.Errorf("/stats %s total_req_count = %d, want %d", backend.ID, rows[i].TotalReqCount, wantTotal[backend.ID])
		}
		if rows[i].IsHostAlive != wantAlive[backend.ID] {
			t.Errorf("/stats %s is_host_alive = %t, want %t", backend.ID, rows[i].IsHostAlive, wantAlive[backend.ID])
		}
	}

	eventually(t, prometheusPollWindow, "/metrics backend_total_request_count caught up with the proxied traffic", func() error {
		samples, err := scrapeMetrics(s)
		if err != nil {
			return err
		}
		for _, backend := range s.Backends {
			if err := requireBackendGauge(samples, "backend_total_request_count", backend, float64(wantTotal[backend.ID])); err != nil {
				return err
			}
		}
		return nil
	})
}
