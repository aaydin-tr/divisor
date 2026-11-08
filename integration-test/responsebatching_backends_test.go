package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A Backend dies and another hangs while traffic flows: requests divisor routes to the dead one get 502, to the
// hanging one 504 within proxy_timeout plus a margin, and those to the healthy one are answered as before.
func TestOneCPUGatewayErrorsUnderLoadLeaveTheHealthyBackendAlone(t *testing.T) {
	t.Parallel()
	const proxyTimeout = 3 * time.Second
	const margin = 5 * time.Second
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:         "rb-gwerrors",
		Type:         "round-robin",
		ProxyTimeout: proxyTimeout,
		Backends:     []BackendSpec{{ID: "healthy"}, {ID: "dead"}, {ID: "hanging"}},
	}, func(t *testing.T, s *Scenario) {
		s.KillBackend(t, "dead")
		s.PauseBackend(t, "hanging")
		t.Cleanup(func() { pool.Client.UnpauseContainer(s.Backend("hanging").Resource.Container.ID) })

		load := startBurstLoad(t, s, 24, 100*time.Millisecond, sameEchoRequest("/under-gateway-errors"))
		time.Sleep(8 * time.Second)
		outcomes := load.stop()
		logLoadSummary(t, outcomes)

		statusCounts := map[int]int{}
		var unexpected []requestOutcome
		for _, o := range outcomes {
			statusCounts[o.status]++
			var expected bool
			switch o.status {
			case http.StatusOK:
				// A healthy answer never waits behind the hanging Backend.
				expected = o.echoedOK() && o.backend == "healthy" && o.latency < proxyTimeout
			case http.StatusBadGateway:
				expected = o.latency <= proxyTimeout+margin
			case http.StatusGatewayTimeout:
				expected = o.latency >= proxyTimeout*3/4 && o.latency <= proxyTimeout+margin
			}
			if !expected {
				unexpected = append(unexpected, o)
			}
		}
		if len(unexpected) > 0 {
			t.Errorf("%d of %d requests got an answer that is not the healthy Backend's quick 200, a 502 or a 504 at proxy_timeout (%s); first ones: %s",
				len(unexpected), len(outcomes), proxyTimeout, describeFirstOutcomes(unexpected))
		}
		for _, status := range []int{http.StatusOK, http.StatusBadGateway, http.StatusGatewayTimeout} {
			if statusCounts[status] == 0 {
				t.Errorf("no request got %d; round-robin must have routed some to each Backend (statuses: %v)", status, statusCounts)
			}
		}
	})
}

// A pod dies at peak traffic: with Probes every second the killed Backend leaves the rotation within a few Probe
// rounds and gets traffic again once restarted, so the Probe loop keeps running next to the request handlers.
func TestOneCPUFailoverUnderLoad(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:              "rb-failover",
		Type:              "round-robin",
		HealthCheckerTime: time.Second,
		// The restarted container may get another IP, as a restarted pod does.
		DNSCacheDuration: time.Second,
		Backends:         []BackendSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}, func(t *testing.T, s *Scenario) {
		load := startBurstLoad(t, s, 32, 200*time.Millisecond, sameEchoRequest("/failover"))
		eventually(t, 10*time.Second, "the load reaches every Backend", func() error {
			if servedBy := countByBackend(load.outcomesSoFar()); len(servedBy) < 3 {
				return fmt.Errorf("load served by %v", servedBy)
			}
			return nil
		})

		killedAt := time.Now()
		s.KillBackend(t, "c")
		eventuallyBackendAlive(t, s, "c", false, 20*time.Second)
		// The Pool marks a Backend Down only after it left the Balancer, so no request sent from here on can pick it.
		leftAt := time.Now()
		// Long enough for divisor's dialer to drop the killed container's IP (2x dns_cache_duration after its last use).
		time.Sleep(3 * time.Second)

		restartedAt := time.Now()
		s.RestartBackend(t, "c")
		eventually(t, 25*time.Second, "the restarted Backend gets traffic again", func() error {
			if servedBy := countByBackend(sentAfter(load.outcomesSoFar(), restartedAt)); servedBy["c"] == 0 {
				return fmt.Errorf("since the restart the load was served by %v", servedBy)
			}
			return nil
		})
		outcomes := load.stop()
		logLoadSummary(t, outcomes)

		var unexpected []requestOutcome
		for _, o := range outcomes {
			// Requests divisor routes to the killed Backend while it is still in the rotation get 502.
			unansweredAtTheKill := o.sentAt.Add(o.latency).After(killedAt)
			if o.echoedOK() || (o.status == http.StatusBadGateway && unansweredAtTheKill && o.sentAt.Before(leftAt)) {
				continue
			}
			unexpected = append(unexpected, o)
		}
		if len(unexpected) > 0 {
			t.Errorf("%d of %d requests failed outside the time the killed Backend was still in the rotation; first ones: %s",
				len(unexpected), len(outcomes), describeFirstOutcomes(unexpected))
		}
		var routedToTheDownBackend int
		for _, o := range sentAfter(outcomes, leftAt) {
			if o.backend == "c" && o.sentAt.Before(restartedAt) {
				routedToTheDownBackend++
			}
		}
		if routedToTheDownBackend > 0 {
			t.Errorf("%d requests reached Backend c between its eviction and its restart", routedToTheDownBackend)
		}
	})
}

// eventuallyBackendAlive waits for /stats to report the Backend Alive or Down.
func eventuallyBackendAlive(t *testing.T, s *Scenario, id string, alive bool, timeout time.Duration) {
	t.Helper()
	address := backendAddress(s.Backend(id))
	eventually(t, timeout, fmt.Sprintf("/stats reports Backend %s is_host_alive=%t", id, alive), func() error {
		rows, err := fetchBackendStats(s)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Addr == address && row.IsHostAlive == alive {
				return nil
			}
		}
		return fmt.Errorf("/stats rows: %+v", rows)
	})
}

// A slow and a fast Backend under concurrent load: least-response-time sends the fast one clearly more, so the
// Scores measure the Backends, not the time a request waits for the core.
func TestOneCPULeastResponseTimeFavorsTheFastBackendUnderLoad(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name: "rb-lrt",
		Type: "least-response-time",
		Backends: []BackendSpec{
			{ID: "fast"},
			{ID: "slow", ResponseDelay: 100 * time.Millisecond},
		},
	}, func(t *testing.T, s *Scenario) {
		outcomes := runConcurrentClients(s, 16, 40, sameEchoRequest("/least-response-time"))
		logLoadSummary(t, outcomes)
		requireEveryRequestEchoed(t, outcomes)
		servedBy := countByBackend(outcomes)
		if servedBy["fast"] < 3*servedBy["slow"] {
			t.Errorf("the fast Backend served %d requests and the slow one %d; want the fast one at least 3 times as many",
				servedBy["fast"], servedBy["slow"])
		}
	})
}

// A ConfigMap update at peak traffic: a Reload (SIGHUP) adding a Backend fails no request, and the added Backend
// starts getting traffic.
func TestOneCPUReloadUnderLoad(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-reload",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
	}, func(t *testing.T, s *Scenario) {
		load := startBurstLoad(t, s, 32, 100*time.Millisecond, sameEchoRequest("/reload"))
		time.Sleep(time.Second)

		s.addBackend(t, BackendSpec{ID: "b"})
		s.rewriteDivisorConfig(t)
		reloadedAt := time.Now()
		s.ReloadDivisor(t)
		eventuallyLogged(t, s, `"msg":"Reload applied"`, `"trigger":"SIGHUP"`)
		eventually(t, 15*time.Second, "the added Backend gets traffic from the load", func() error {
			if servedBy := countByBackend(sentAfter(load.outcomesSoFar(), reloadedAt)); servedBy["b"] == 0 {
				return fmt.Errorf("since the Reload the load was served by %v", servedBy)
			}
			return nil
		})
		time.Sleep(time.Second)

		outcomes := load.stop()
		logLoadSummary(t, outcomes)
		requireEveryRequestEchoed(t, outcomes)
		if servedBy := countByBackend(sentAfter(outcomes, reloadedAt)); servedBy["a"] == 0 {
			t.Errorf("Backend a got no traffic after the Reload (served by: %v)", servedBy)
		}
	})
}
