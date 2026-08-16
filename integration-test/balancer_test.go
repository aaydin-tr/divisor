package integration

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// countBackends fires n sequential GETs and tallies which Backend served
// each. Every request must succeed.
func countBackends(t *testing.T, s *Scenario, n int, path string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		counts[s.BackendOf(t, fmt.Sprintf("%s?n=%d", path, i))]++
	}
	return counts
}

func TestRoundRobinDistribution(t *testing.T) {
	s := startScenario(t, ScenarioSpec{
		Name: "rr",
		Type: "round-robin",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	var order []string
	for i := range 30 {
		order = append(order, s.BackendOf(t, fmt.Sprintf("/rr?n=%d", i)))
	}

	counts := map[string]int{}
	for _, b := range order {
		counts[b]++
	}
	for _, id := range []string{"a", "b", "c"} {
		if counts[id] != 10 {
			t.Errorf("backend %s served %d of 30 requests, want exactly 10 (counts: %v)", id, counts[id], counts)
		}
	}
	for i := 1; i < len(order); i++ {
		if order[i] == order[i-1] {
			t.Errorf("requests %d and %d both served by %s; round-robin must rotate", i-1, i, order[i])
		}
	}
}

func TestWeightedRoundRobinDistribution(t *testing.T) {
	s := startScenario(t, ScenarioSpec{
		Name: "wrr",
		Type: "w-round-robin",
		Backends: []BackendSpec{
			{ID: "a", Weight: 3}, {ID: "b", Weight: 2}, {ID: "c", Weight: 1},
		},
	})

	counts := countBackends(t, s, 60, "/wrr")

	want := map[string]int{"a": 30, "b": 20, "c": 10}
	for id, w := range want {
		if counts[id] != w {
			t.Errorf("backend %s served %d of 60 requests, want exactly %d (weights 3/2/1, counts: %v)", id, counts[id], w, counts)
		}
	}
}

func TestRandomDistribution(t *testing.T) {
	s := startScenario(t, ScenarioSpec{
		Name: "random",
		Type: "random",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	const total = 300
	counts := countBackends(t, s, total, "/random")

	sum := 0
	for _, id := range []string{"a", "b", "c"} {
		if counts[id] == 0 {
			t.Errorf("backend %s never served a request in %d; random should reach every backend (counts: %v)", id, total, counts)
		}
		sum += counts[id]
	}
	if sum != total {
		t.Errorf("responses came from unexpected backends: counted %d of %d (counts: %v)", sum, total, counts)
	}
}

func TestLeastConnection(t *testing.T) {
	s := startScenario(t, ScenarioSpec{
		Name: "lc",
		Type: "least-connection",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	// Occupy two backends with held (slow) requests, then check that quick
	// traffic flows to the remaining free backend. least-connection tracks
	// in-flight requests (fasthttp PendingRequests), so the held requests
	// pin their backends at 1 while the free one sits at 0.
	// The workers must not call t.Fatalf (it only Goexits the worker), so
	// they report through the channel and the test goroutine judges.
	type heldResult struct {
		backend string
		err     error
	}
	var wg sync.WaitGroup
	held := make(chan heldResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.Request(http.MethodGet, fmt.Sprintf("/held?delay=4s&h=%d", i), nil, nil)
			switch {
			case err != nil:
				held <- heldResult{err: err}
			case res.StatusCode != http.StatusOK || res.Echo == nil:
				held <- heldResult{err: fmt.Errorf("held request got status %d", res.StatusCode)}
			default:
				held <- heldResult{backend: res.Echo.Backend}
			}
		}(i)
	}
	time.Sleep(700 * time.Millisecond) // both held requests are in flight now

	quick := countBackends(t, s, 20, "/quick")

	wg.Wait()
	close(held)
	heldBackends := map[string]bool{}
	for h := range held {
		if h.err != nil {
			t.Fatalf("held request failed: %v", h.err)
		}
		heldBackends[h.backend] = true
	}
	if len(heldBackends) != 2 {
		t.Fatalf("held requests landed on %d distinct backends, want 2 (least-connection should spread them)", len(heldBackends))
	}

	free := ""
	for _, id := range []string{"a", "b", "c"} {
		if !heldBackends[id] {
			free = id
		}
	}
	if quick[free] < 18 {
		t.Errorf("free backend %s served only %d of 20 quick requests while %v were occupied (counts: %v)", free, quick[free], heldBackends, quick)
	}
}

func TestLeastResponseTime(t *testing.T) {
	s := startScenario(t, ScenarioSpec{
		Name: "lrt",
		Type: "least-response-time",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
			{ID: "slow", ResponseDelay: 400 * time.Millisecond},
		},
	})

	// Warmup lets divisor learn each backend's response time; the slow
	// backend gets picked while its average is still zero, then its ~400ms
	// average should keep all measured traffic away from it.
	for i := 0; i < 10; i++ {
		s.Get(t, fmt.Sprintf("/warmup?n=%d", i))
	}

	counts := countBackends(t, s, 30, "/measured")
	if counts["slow"] != 0 {
		t.Errorf("slow backend served %d of 30 measured requests, want 0 once its response time is known (counts: %v)", counts["slow"], counts)
	}
	if counts["a"]+counts["b"] != 30 {
		t.Errorf("fast backends served %d of 30 measured requests, want all 30 (counts: %v)", counts["a"]+counts["b"], counts)
	}
}
