package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestFailoverAfterKill(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:              "fokill",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	baseline := countBackends(t, s, 30, "/baseline")
	for _, id := range []string{"a", "b", "c"} {
		if baseline[id] == 0 {
			t.Fatalf("backend %s got no traffic before the kill (counts: %v)", id, baseline)
		}
	}

	// Hard container death: connection refused, the Probe evicts it within
	// a cycle. (Rejoin-after-restart is deliberately NOT asserted here; the
	// container's IP can change across restarts. Rejoin is covered by the
	// health-toggle and pause tests, which keep the same endpoint.)
	s.KillBackend(t, "c")

	eventually(t, 15*time.Second, "traffic converged on the survivors", func() error {
		counts, err := pollBackends(s, 10, "/postkill")
		if err != nil {
			return err
		}
		if counts["c"] > 0 {
			return fmt.Errorf("dead backend c still in rotation (counts: %v)", counts)
		}
		return nil
	})

	counts := countBackends(t, s, 30, "/steady")
	if counts["c"] != 0 || counts["a"]+counts["b"] != 30 {
		t.Errorf("steady-state distribution after failover is %v, want a+b only", counts)
	}
}

func TestHealthToggleFailoverAndRejoin(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:              "fotog",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	// A failing Probe (503) must take the backend Down...
	s.Backend("c").SetHealth(t, false)
	eventually(t, 15*time.Second, "Down backend left the rotation", func() error {
		counts, err := pollBackends(s, 10, "/down")
		if err != nil {
			return err
		}
		if counts["c"] > 0 {
			return fmt.Errorf("Down backend c still in rotation (counts: %v)", counts)
		}
		return nil
	})

	// ...and one succeeding Probe must let it Rejoin.
	s.Backend("c").SetHealth(t, true)
	eventually(t, 15*time.Second, "backend c rejoined the rotation", func() error {
		counts, err := pollBackends(s, 12, "/up")
		if err != nil {
			return err
		}
		if counts["c"] == 0 {
			return fmt.Errorf("backend c not yet back in rotation (counts: %v)", counts)
		}
		return nil
	})
}

func TestBackendDownAtStartupCanRejoin(t *testing.T) {
	t.Parallel()
	// SPEC (1.0): a backend that was Down when divisor booted must Rejoin
	// once its Probe succeeds. BORN RED: divisor never adds such a backend
	// to its server map (core/round-robin/round-robin.go healthCheck), so
	// today it can never Rejoin.
	s := startScenario(t, ScenarioSpec{
		Name:              "fostart",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"},
			{ID: "b", StartDown: true},
		},
	})

	counts := countBackends(t, s, 10, "/boot")
	if counts["b"] != 0 {
		t.Fatalf("backend b was Down at startup but received traffic (counts: %v)", counts)
	}

	s.Backend("b").SetHealth(t, true)
	eventually(t, 20*time.Second, "startup-Down backend rejoined once its Probe succeeded (1.0 spec; known bug: it never does)", func() error {
		counts, err := pollBackends(s, 10, "/rejoin")
		if err != nil {
			return err
		}
		if counts["b"] == 0 {
			return fmt.Errorf("backend b still not in rotation (counts: %v)", counts)
		}
		return nil
	})
}

func TestUnreachableBackendGets502(t *testing.T) {
	t.Parallel()
	// SPEC (1.0, grilling Q13): while a dead backend is still in rotation
	// (long Probe interval), requests routed to it must surface 502 Bad
	// Gateway.
	s := startScenario(t, ScenarioSpec{
		Name:              "fo502",
		Type:              "round-robin",
		HealthCheckerTime: 10 * time.Minute, // probes never fire during the test
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	s.KillBackend(t, "b")

	sawFailure := false
	for i := 0; i < 20; i++ {
		res, err := s.Request(http.MethodGet, fmt.Sprintf("/dead?n=%d", i), nil, nil)
		if err != nil {
			t.Fatalf("request %d failed at transport level: %v", i, err)
		}
		if res.StatusCode == http.StatusOK {
			continue
		}
		sawFailure = true
		if res.StatusCode != http.StatusBadGateway {
			t.Fatalf("request %d to the Down Backend got %d, want 502 Bad Gateway (1.0 spec)", i, res.StatusCode)
		}
	}
	if !sawFailure {
		t.Fatalf("no request was routed to the dead backend; round-robin should have alternated onto it")
	}
}

func TestAllBackendsDownStaysUp(t *testing.T) {
	t.Parallel()
	// SPEC (1.0, grilling Q14): with every backend Down, divisor must stay
	// up, answer with a gateway error, and let backends Rejoin. Born red
	// (the health checker used to panic("All backends are down")); green
	// since the balancers started serving 503 on an empty rotation.
	s := startScenario(t, ScenarioSpec{
		Name:              "foall",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	s.Backend("a").SetHealth(t, false)
	s.Backend("b").SetHealth(t, false)
	time.Sleep(4 * time.Second) // at least one probe cycle

	for i := 0; i < 5; i++ {
		res, err := s.Request(http.MethodGet, fmt.Sprintf("/alldown?n=%d", i), nil, nil)
		if err != nil {
			t.Fatalf("divisor stopped answering with all backends down (1.0 spec: stay up and serve gateway errors): %v", err)
		}
		if res.StatusCode != http.StatusBadGateway && res.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("request %d with all backends down got %d, want 502 or 503", i, res.StatusCode)
		}
	}

	// Rejoin after total outage: one backend comes back, traffic resumes.
	s.Backend("a").SetHealth(t, true)
	eventually(t, 15*time.Second, "backend rejoined after a total outage", func() error {
		res, err := s.Request(http.MethodGet, "/afteroutage", nil, nil)
		if err != nil {
			return err
		}
		if res.StatusCode != http.StatusOK || res.Echo == nil {
			return fmt.Errorf("status %d", res.StatusCode)
		}
		return nil
	})
}

func TestPausedBackendBoundedFailure(t *testing.T) {
	t.Parallel()
	specRed(t, "proxy timeout -> bounded failure for hanging backends")
	// A paused container hangs instead of refusing connections. With
	// server read/write timeouts configured, clients must see a bounded
	// failure, and the Probe (5s timeout) must eventually evict the
	// backend. Pause keeps the container's IP, so unpause exercises Rejoin.
	s := startScenario(t, ScenarioSpec{
		Name:              "fopause",
		Type:              "round-robin",
		HealthCheckerTime: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	s.PauseBackend(t, "b")

	cl := s.NewClient(10 * time.Second)
	for i := 0; i < 6; i++ {
		start := time.Now()
		resp, err := cl.Get(fmt.Sprintf("%s/paused?n=%d", s.BaseURL, i))
		if err == nil {
			resp.Body.Close()
		}
		if elapsed := time.Since(start); elapsed > 8*time.Second {
			t.Errorf("request %d took %s; a hanging backend must fail within the configured timeouts", i, elapsed)
		}
	}

	eventually(t, 25*time.Second, "hanging backend was evicted by its Probe", func() error {
		counts, err := pollBackends(s, 10, "/evicted")
		if err != nil {
			return err
		}
		if counts["b"] > 0 {
			return fmt.Errorf("paused backend still in rotation (counts: %v)", counts)
		}
		return nil
	})

	s.UnpauseBackend(t, "b")
	eventually(t, 15*time.Second, "unpaused backend rejoined", func() error {
		counts, err := pollBackends(s, 10, "/unpaused")
		if err != nil {
			return err
		}
		if counts["b"] == 0 {
			return fmt.Errorf("backend b not yet back (counts: %v)", counts)
		}
		return nil
	})
}
