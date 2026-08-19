package integration

import (
	"net/http"
	"testing"
	"time"
)

// SIGTERM must drain: in-flight requests complete, new connections are
// refused, and the process exits 0 well inside its 30s shutdown budget.
func TestGracefulShutdown(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "shutdown",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
	})

	type outcome struct {
		res *Result
		err error
	}
	inflight := make(chan outcome, 1)
	go func() {
		res, err := s.Request(http.MethodGet, "/slow?delay=3s", nil, nil)
		inflight <- outcome{res, err}
	}()

	time.Sleep(700 * time.Millisecond) // the slow request is in flight now
	s.TerminateDivisor(t)

	select {
	case o := <-inflight:
		if o.err != nil {
			t.Errorf("in-flight request was dropped during graceful shutdown: %v", o.err)
		} else if o.res.StatusCode != http.StatusOK || o.res.Echo == nil {
			t.Errorf("in-flight request got status %d during graceful shutdown, want 200", o.res.StatusCode)
		}
	case <-time.After(15 * time.Second):
		t.Errorf("in-flight request never completed after SIGTERM")
	}

	code := s.WaitDivisorExit(t, 35*time.Second)
	if code != 0 {
		t.Errorf("divisor exited with code %d after SIGTERM, want 0", code)
	}

	cl := s.NewClient(3 * time.Second)
	if resp, err := cl.Get(s.BaseURL + "/afterexit"); err == nil {
		resp.Body.Close()
		t.Errorf("divisor still answered after SIGTERM shutdown completed")
	}
}
