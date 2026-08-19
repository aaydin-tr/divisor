package integration

import (
	"fmt"
	"testing"
	"time"
)

// ip-hash hashes the TCP source IP (core/ip-hash/ip-hash.go), so every
// host-originated request shares one client identity. Stickiness is testable
// from the host; distribution and remapping need ClientContainers, each with
// its own IP on the suite network.

func TestIPHashStickinessFromHost(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name: "ih",
		Type: "ip-hash",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})

	first := s.BackendOf(t, "/sticky?n=0")
	for i := 1; i < 20; i++ {
		got := s.BackendOf(t, fmt.Sprintf("/sticky?n=%d", i))
		if got != first {
			t.Fatalf("request %d served by %s, previous by %s; same client IP must always map to the same backend", i, got, first)
		}
	}
}

func TestIPHashDistributionAndRemap(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:              "ih2",
		Type:              "ip-hash",
		HealthCheckerTime: time.Second,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	})
	clients := startClientContainers(t, s, 8)
	url := s.InternalURL() + "/"

	// Baseline mapping; each client must be sticky.
	mapping := make([]string, len(clients))
	for i, c := range clients {
		mapping[i] = c.CurlEcho(t, url).Backend
		for j := 0; j < 2; j++ {
			if got := c.CurlEcho(t, url).Backend; got != mapping[i] {
				t.Fatalf("client %d flapped between %s and %s; ip-hash must be sticky per client IP", i, mapping[i], got)
			}
		}
	}
	distinct := map[string]bool{}
	for _, b := range mapping {
		distinct[b] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("8 distinct client IPs all mapped to %v; ip-hash is not distributing (is the client IP reaching the hash?)", mapping[0])
	}

	// Take the victim backend Down: only its clients may remap.
	victim := mapping[0]
	s.Backend(victim).SetHealth(t, false)

	eventually(t, 15*time.Second, "victim's clients remapped after their backend went Down", func() error {
		echo, err := clients[0].curlEchoErr(url)
		if err != nil {
			return err
		}
		if echo.Backend == victim {
			return fmt.Errorf("client 0 still mapped to Down backend %s", victim)
		}
		return nil
	})
	remapped := make([]string, len(clients))
	for i, c := range clients {
		remapped[i] = c.CurlEcho(t, url).Backend
		if remapped[i] == victim {
			t.Errorf("client %d still served by down backend %s", i, victim)
		}
		if mapping[i] != victim && remapped[i] != mapping[i] {
			t.Errorf("client %d moved from %s to %s although its backend never went down; consistent hashing must only remap the victim's clients", i, mapping[i], remapped[i])
		}
	}

	// Rejoin: the victim comes back; its clients return, nobody else moves.
	s.Backend(victim).SetHealth(t, true)
	eventually(t, 15*time.Second, "victim's clients returned after their backend rejoined", func() error {
		echo, err := clients[0].curlEchoErr(url)
		if err != nil {
			return err
		}
		if echo.Backend != mapping[0] {
			return fmt.Errorf("client 0 maps to %s, want original %s", echo.Backend, mapping[0])
		}
		return nil
	})
	for i, c := range clients {
		if got := c.CurlEcho(t, url).Backend; got != mapping[i] {
			t.Errorf("after rejoin client %d maps to %s, want original %s", i, got, mapping[i])
		}
	}
}
