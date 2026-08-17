package integration

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Divisor is a TLS-terminating proxy (see CONTEXT.md): TLS exists only on
// the client->divisor edge, backends always speak plain HTTP.
func TestTLSTermination(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name: "tls",
		Type: "round-robin",
		TLS:  true,
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
	})

	t.Run("HandshakeWithSuiteCA", func(t *testing.T) {
		res := s.Get(t, "/tls")
		if res.TLS == nil {
			t.Fatalf("response did not arrive over TLS")
		}
		if res.ProtoMajor != 1 {
			t.Errorf("negotiated HTTP/%d, want HTTP/1.x on the fasthttp TLS path", res.ProtoMajor)
		}
	})

	t.Run("NoH2ALPNOnHTTP1Path", func(t *testing.T) {
		// The scenario's own client never offers h2, so asserting on its
		// response would be vacuous; a raw handshake that explicitly offers
		// h2 proves the http1 path does not negotiate it.
		cp := x509.NewCertPool()
		cp.AppendCertsFromPEM(s.Certs.CAPEM)
		conn, err := tls.Dial("tcp", strings.TrimPrefix(s.BaseURL, "https://"), &tls.Config{
			RootCAs:    cp,
			ServerName: "localhost",
			NextProtos: []string{"h2", "http/1.1"},
		})
		if err != nil {
			t.Fatalf("handshake offering h2 failed: %v", err)
		}
		defer conn.Close()
		if p := conn.ConnectionState().NegotiatedProtocol; p == "h2" {
			t.Errorf("http_version http1 negotiated h2 via ALPN; it must stay HTTP/1.1")
		}
	})

	t.Run("UntrustedCAIsRejected", func(t *testing.T) {
		// A client that trusts a different CA must fail the handshake:
		// proves divisor serves the configured cert, not something anonymous.
		otherCA := generateCerts(t)
		cp := x509.NewCertPool()
		cp.AppendCertsFromPEM(otherCA.CAPEM)
		cl := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: cp}},
			Timeout:   10 * time.Second,
		}
		resp, err := cl.Get(s.BaseURL + "/wrongca")
		if err == nil {
			resp.Body.Close()
			t.Fatalf("handshake with an untrusted CA succeeded; certificate verification is broken")
		}
		if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
			t.Errorf("expected a certificate verification error, got: %v", err)
		}
	})

	t.Run("PlaintextToTLSPortFails", func(t *testing.T) {
		plainURL := "http://" + strings.TrimPrefix(s.BaseURL, "https://")
		cl := &http.Client{Timeout: 5 * time.Second}
		resp, err := cl.Get(plainURL + "/plain")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("plaintext HTTP on the TLS port returned 200; the listener is not actually terminating TLS")
			}
			t.Logf("plaintext request got status %d instead of a transport error", resp.StatusCode)
		}
	})
}
