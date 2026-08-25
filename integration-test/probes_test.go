package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// monitoringStatus probes a monitoring endpoint the way a kubelet does: over
// the network, judging only the status code.
func monitoringStatus(s *Scenario, path string) (int, error) {
	resp, err := ctlClient.Get("http://" + s.MonitoringAddr + path)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// requireProbesAnswer200 asserts both probe endpoints answer 200; during
// names the moment under test in the failure message.
func requireProbesAnswer200(t *testing.T, s *Scenario, during string) {
	t.Helper()
	for _, path := range []string{"/healthz", "/ready"} {
		code, err := monitoringStatus(s, path)
		if err != nil {
			t.Fatalf("probing %s %s: %v", path, during, err)
		}
		if code != http.StatusOK {
			t.Errorf("%s answered %d %s, want 200", path, code, during)
		}
	}
}

func TestProbeEndpointsDuringServeAndShutdown(t *testing.T) {
	t.Parallel()
	// SPEC (1.0): kubelet-shaped probing — /healthz answers 200 whenever the
	// process runs; /ready answers 200 while serving and flips to 503 the
	// moment graceful shutdown begins, while in-flight requests are still
	// completing.
	s := startScenario(t, ScenarioSpec{
		Name:             "probes",
		Type:             "round-robin",
		ExposeMonitoring: true,
		Backends:         []BackendSpec{{ID: "a"}},
	})

	requireProbesAnswer200(t, s, "while serving")

	// A slow request keeps the drain window open long enough to observe the
	// readiness flip.
	type outcome struct {
		res *Result
		err error
	}
	inflight := make(chan outcome, 1)
	go func() {
		res, err := s.Request(http.MethodGet, "/slow?delay=4s", nil, nil)
		inflight <- outcome{res, err}
	}()

	time.Sleep(700 * time.Millisecond) // the slow request is in flight now
	s.TerminateDivisor(t)

	eventually(t, 3*time.Second, "/ready flipped to 503 when graceful shutdown began", func() error {
		readyCode, err := monitoringStatus(s, "/ready")
		if err != nil {
			return err
		}
		if readyCode != http.StatusServiceUnavailable {
			return fmt.Errorf("/ready answered %d, want 503", readyCode)
		}
		healthzCode, err := monitoringStatus(s, "/healthz")
		if err != nil {
			return err
		}
		if healthzCode != http.StatusOK {
			return fmt.Errorf("/healthz answered %d during the drain, want 200", healthzCode)
		}
		return nil
	})

	select {
	case o := <-inflight:
		if o.err != nil {
			t.Errorf("in-flight request was dropped during graceful shutdown: %v", o.err)
		} else if o.res.StatusCode != http.StatusOK {
			t.Errorf("in-flight request got status %d during graceful shutdown, want 200", o.res.StatusCode)
		}
	case <-time.After(15 * time.Second):
		t.Errorf("in-flight request never completed after SIGTERM")
	}

	if code := s.WaitDivisorExit(t, 35*time.Second); code != 0 {
		t.Errorf("divisor exited with code %d after SIGTERM, want 0", code)
	}
}
