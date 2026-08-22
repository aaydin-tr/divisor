package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func testBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestProxyMatrix drives the full HTTP/1.1 proxy surface through one shared
// round-robin Scenario. Subtests run sequentially and label their own
// requests, so they never depend on cumulative state.
func TestProxyMatrix(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name: "proxy",
		Type: "round-robin",
		Backends: []BackendSpec{
			{ID: "a"}, {ID: "b"},
		},
		CustomHeaders: map[string]string{
			"X-Req-Id":    "$uuid",
			"X-Client-Ip": "$remote_addr",
			"X-Req-Time":  "$time",
			"X-Req-Seq":   "$incremental",
		},
	})

	t.Run("MethodsWithBodies", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			var body []byte
			if method != http.MethodGet {
				body = []byte("payload-" + method)
			}
			res := s.MustEcho(t, method, "/methods?m="+method, body, nil)
			if res.Echo.Method != method {
				t.Errorf("backend saw method %s, want %s", res.Echo.Method, method)
			}
			if body != nil {
				if res.Echo.BodySha256 != sha256Hex(body) {
					t.Errorf("%s body arrived corrupted at backend (len %d, want %d)", method, res.Echo.BodyLen, len(body))
				}
				if !bytes.Equal(res.Echo.Body(t), body) {
					t.Errorf("%s echoed body differs from sent body", method)
				}
			}
		}
	})

	t.Run("Head", func(t *testing.T) {
		res, err := s.Request(http.MethodHead, "/head", nil, nil)
		if err != nil {
			t.Fatalf("HEAD failed: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("HEAD status %d, want 200", res.StatusCode)
		}
		if len(res.Body) != 0 {
			t.Errorf("HEAD response has %d body bytes, want none", len(res.Body))
		}
		if res.Header.Get("X-Backend-Id") == "" {
			t.Errorf("HEAD response missing X-Backend-Id; headers did not pass through")
		}
	})

	t.Run("Options", func(t *testing.T) {
		res := s.MustEcho(t, http.MethodOptions, "/options", nil, nil)
		if res.Echo.Method != http.MethodOptions {
			t.Errorf("backend saw method %s, want OPTIONS", res.Echo.Method)
		}
	})

	t.Run("EmptyBody", func(t *testing.T) {
		res := s.MustEcho(t, http.MethodPost, "/empty", []byte{}, nil)
		if res.Echo.BodyLen != 0 {
			t.Errorf("backend saw %d body bytes for an empty POST", res.Echo.BodyLen)
		}
	})

	t.Run("LargeBody1MB", func(t *testing.T) {
		body := testBody(1 << 20)
		res := s.MustEcho(t, http.MethodPost, "/large1mb", body, nil)
		if res.Echo.BodyLen != len(body) {
			t.Errorf("backend received %d bytes, want %d", res.Echo.BodyLen, len(body))
		}
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("1MB body arrived corrupted at backend")
		}
	})

	t.Run("LargeBody3MBUnderLimit", func(t *testing.T) {
		body := testBody(3 << 20)
		res := s.MustEcho(t, http.MethodPost, "/large3mb", body, nil)
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("3MB body arrived corrupted at backend")
		}
	})

	t.Run("BodyOverLimit413", func(t *testing.T) {
		// server.max_request_body_size defaults to 4MB. The client must
		// observe a failure and the payload must never reach a backend;
		// fasthttp may reset the connection mid-upload instead of
		// delivering the 413 cleanly.
		body := testBody(5 << 20)
		res, err := s.Request(http.MethodPost, "/toolarge", bytes.NewReader(body), nil)
		if err == nil {
			if res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Errorf("5MB POST got status %d, want 413", res.StatusCode)
			}
			if res.Echo != nil {
				t.Errorf("5MB POST reached backend %s; the size limit did not apply", res.Echo.Backend)
			}
		} else {
			t.Logf("5MB POST failed at transport level instead of a clean 413: %v", err)
		}
	})

	t.Run("ChunkedBodyOverLimit", func(t *testing.T) {
		// No Content-Length, so the limit must trip mid-stream.
		body := testBody(5 << 20)
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/chunkedtoolarge", rd, nil)
		if err != nil {
			t.Logf("oversized chunked POST failed at transport level instead of a clean 413: %v", err)
			return
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized chunked POST got status %d, want 413", res.StatusCode)
		}
		if res.Echo != nil {
			t.Errorf("oversized chunked POST reached backend %s", res.Echo.Backend)
		}
	})

	t.Run("ChunkedRequest", func(t *testing.T) {
		body := testBody(128 << 10)
		// Hide the reader's type so net/http cannot set Content-Length and
		// must use chunked transfer encoding.
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/chunked", rd, nil)
		if err != nil {
			t.Fatalf("chunked POST failed: %v", err)
		}
		if res.StatusCode != http.StatusOK || res.Echo == nil {
			t.Fatalf("chunked POST: status %d, body %.200s", res.StatusCode, res.Body)
		}
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("chunked body arrived corrupted at backend")
		}
	})

	t.Run("RequestHopByHopStripped", func(t *testing.T) {
		// net/http refuses to send hop-by-hop headers, so drive this one
		// with a raw fasthttp client.
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		res := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(res)

		req.SetRequestURI(s.BaseURL + "/hophdr")
		req.Header.Set("Proxy-Authorization", "secret")
		req.Header.Set("Keep-Alive", "timeout=5")
		req.Header.Set("Te", "trailers")
		req.Header.Set("X-Stays", "yes")

		if err := (&fasthttp.Client{}).Do(req, res); err != nil {
			t.Fatalf("raw request failed: %v", err)
		}
		var echo EchoResp
		if err := decodeJSON(bytes.NewReader(res.Body()), &echo); err != nil {
			t.Fatalf("response is not an echo reply: %.200s", res.Body())
		}
		for _, h := range []string{"Proxy-Authorization", "Keep-Alive", "Te"} {
			if echo.Header(h) != "" {
				t.Errorf("hop-by-hop header %s leaked to the backend with value %q", h, echo.Header(h))
			}
		}
		if echo.Header("X-Stays") != "yes" {
			t.Errorf("end-to-end header X-Stays was lost")
		}
	})

	t.Run("ResponseHopByHopStripped", func(t *testing.T) {
		res := s.Get(t, "/resphdr?rh=Keep-Alive:timeout%3D5&rh=Upgrade:websocket&rh=X-Resp-Stays:1")
		if res.Header.Get("X-Resp-Stays") != "1" {
			t.Errorf("end-to-end response header X-Resp-Stays was lost")
		}
		for _, h := range []string{"Keep-Alive", "Upgrade"} {
			if res.Header.Get(h) != "" {
				t.Errorf("hop-by-hop response header %s leaked to the client with value %q", h, res.Header.Get(h))
			}
		}
	})

	t.Run("RequestConnectionNominatedStripped", func(t *testing.T) {
		// RFC 9110 §7.6.1: headers named in Connection are hop-by-hop even
		// when they are not on the classic list. Raw fasthttp client, since
		// net/http's Transport rewrites Connection itself.
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		res := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(res)

		req.SetRequestURI(s.BaseURL + "/connhdr")
		req.Header.Set("Connection", "X-Secret, X-Also-Secret")
		req.Header.Set("X-Secret", "s3cr3t")
		req.Header.Set("X-Also-Secret", "too")
		req.Header.Set("X-Stays", "yes")

		if err := (&fasthttp.Client{}).Do(req, res); err != nil {
			t.Fatalf("raw request failed: %v", err)
		}
		var echo EchoResp
		if err := decodeJSON(bytes.NewReader(res.Body()), &echo); err != nil {
			t.Fatalf("response is not an echo reply: %.200s", res.Body())
		}
		for _, h := range []string{"X-Secret", "X-Also-Secret", "Connection"} {
			if echo.Header(h) != "" {
				t.Errorf("Connection-nominated header %s leaked to the backend with value %q", h, echo.Header(h))
			}
		}
		if echo.Header("X-Stays") != "yes" {
			t.Errorf("end-to-end header X-Stays was lost")
		}
	})

	t.Run("ResponseConnectionNominatedStripped", func(t *testing.T) {
		res := s.Get(t, "/connresp?rh=Connection:X-Internal-Debug&rh=X-Internal-Debug:pool%3D3&rh=X-Resp-Stays:1")
		if res.Header.Get("X-Resp-Stays") != "1" {
			t.Errorf("end-to-end response header X-Resp-Stays was lost")
		}
		if v := res.Header.Get("X-Internal-Debug"); v != "" {
			t.Errorf("Connection-nominated response header X-Internal-Debug leaked to the client with value %q", v)
		}
	})

	t.Run("XForwardedFor", func(t *testing.T) {
		// Pinned current behavior: divisor OVERWRITES client-supplied
		// X-Forwarded-For with the direct peer IP (anti-spoofing for an
		// edge balancer). Revisit for 1.0 if append semantics are wanted.
		hdr := http.Header{"X-Forwarded-For": []string{"1.2.3.4"}}
		res := s.MustEcho(t, http.MethodGet, "/xff", nil, hdr)
		xff := res.Echo.Header("X-Forwarded-For")
		if net.ParseIP(xff) == nil {
			t.Errorf("X-Forwarded-For at backend is %q, want a valid IP", xff)
		}
		if xff == "1.2.3.4" {
			t.Errorf("client-supplied X-Forwarded-For was trusted verbatim; divisor should replace it with the peer IP")
		}
	})

	t.Run("CustomHeaders", func(t *testing.T) {
		seqByBackend := map[string][]uint64{}
		seen := map[string]bool{}
		for i := 0; i < 6; i++ {
			res := s.MustEcho(t, http.MethodGet, fmt.Sprintf("/custom?n=%d", i), nil, nil)
			e := res.Echo

			id := e.Header("X-Req-Id")
			if _, err := uuid.Parse(id); err != nil {
				t.Fatalf("X-Req-Id ($uuid) is %q, not a UUID", id)
			}
			if seen[id] {
				t.Errorf("X-Req-Id %q repeated; $uuid must be unique per request", id)
			}
			seen[id] = true

			if ip := e.Header("X-Client-Ip"); net.ParseIP(ip) == nil {
				t.Errorf("X-Client-Ip ($remote_addr) is %q, want a valid IP", ip)
			}
			ts := e.Header("X-Req-Time")
			if _, err := time.Parse("2006-01-02T15:04:05.000Z", ts); err != nil {
				t.Errorf("X-Req-Time ($time) is %q, not in divisor's documented layout", ts)
			}
			var seq uint64
			if _, err := fmt.Sscanf(e.Header("X-Req-Seq"), "%d", &seq); err != nil {
				t.Errorf("X-Req-Seq ($incremental) is %q, not a number", e.Header("X-Req-Seq"))
			}
			seqByBackend[e.Backend] = append(seqByBackend[e.Backend], seq)
		}
		// $incremental is a per-backend counter; within one backend it must
		// strictly increase.
		for backend, seqs := range seqByBackend {
			for i := 1; i < len(seqs); i++ {
				if seqs[i] <= seqs[i-1] {
					t.Errorf("backend %s saw $incremental sequence %v; values must strictly increase", backend, seqs)
				}
			}
		}
	})

	t.Run("BackendErrorPassthrough", func(t *testing.T) {
		for _, code := range []int{500, 404} {
			res, err := s.Request(http.MethodGet, fmt.Sprintf("/err?status=%d", code), nil, nil)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if res.StatusCode != code {
				t.Errorf("backend returned %d but client saw %d; backend statuses must pass through untouched", code, res.StatusCode)
			}
			if res.Echo == nil {
				t.Errorf("backend %d response body was replaced; bodies must pass through untouched: %.200s", code, res.Body)
			}
		}
	})

	t.Run("KeepAliveReuse", func(t *testing.T) {
		cl := s.NewClient(10 * time.Second)
		do := func(trace *httptrace.ClientTrace) {
			req, err := http.NewRequest(http.MethodGet, s.BaseURL+"/ka", nil)
			if err != nil {
				t.Fatal(err)
			}
			if trace != nil {
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
			}
			resp, err := cl.Do(req)
			if err != nil {
				t.Fatalf("keep-alive request failed: %v", err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		do(nil)
		reused := false
		do(&httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
		if !reused {
			t.Errorf("second request opened a new connection; divisor must support client keep-alive")
		}
	})

	t.Run("ConcurrentRequests", func(t *testing.T) {
		const workers, perWorker = 100, 5
		var (
			mu     sync.Mutex
			counts = map[string]int{}
			errs   []string
		)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for r := 0; r < perWorker; r++ {
					nonce := fmt.Sprintf("worker-%d-req-%d", w, r)
					res, err := s.Request(http.MethodPost, "/conc", bytes.NewReader([]byte(nonce)), nil)
					// No t.Fatalf-based helpers in here: a Fatalf from a
					// worker goroutine would Goexit while holding mu and
					// deadlock the other workers.
					var body []byte
					if err == nil && res.Echo != nil {
						body, _ = base64.StdEncoding.DecodeString(res.Echo.BodyB64)
					}
					mu.Lock()
					switch {
					case err != nil:
						errs = append(errs, fmt.Sprintf("%s: %v", nonce, err))
					case res.StatusCode != http.StatusOK || res.Echo == nil:
						errs = append(errs, fmt.Sprintf("%s: status %d", nonce, res.StatusCode))
					case string(body) != nonce:
						errs = append(errs, fmt.Sprintf("%s: got someone else's response body", nonce))
					default:
						counts[res.Echo.Backend]++
					}
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()

		if len(errs) > 0 {
			t.Fatalf("%d of %d concurrent requests failed; first failures: %v", len(errs), workers*perWorker, errs[:min(5, len(errs))])
		}
		// Tolerance band rather than an exact split: an exact 250/250 is a
		// property of the current single-atomic-counter round-robin, not of
		// round-robin as a contract, and would falsely redden an internally
		// different but behaviorally fair 1.0 scheduler.
		total := workers * perWorker
		if counts["a"]+counts["b"] != total {
			t.Errorf("responses came from unexpected backends (counts: %v, want %d total)", counts, total)
		}
		for _, id := range []string{"a", "b"} {
			if counts[id] < total*45/100 || counts[id] > total*55/100 {
				t.Errorf("backend %s served %d of %d concurrent requests, want a fair share within 45-55%% (counts: %v)", id, counts[id], total, counts)
			}
		}
	})

	t.Run("QueryAndPathEscaping", func(t *testing.T) {
		res := s.MustEcho(t, http.MethodGet, "/esc%20aped?q=hello%26world&x=1%2B2", nil, nil)
		if res.Echo.Path != "/esc aped" {
			t.Errorf("backend saw path %q, want %q", res.Echo.Path, "/esc aped")
		}
		if res.Echo.RawQuery != "q=hello%26world&x=1%2B2" {
			t.Errorf("backend saw raw query %q; escaping must be preserved byte-for-byte", res.Echo.RawQuery)
		}
	})

	t.Run("IdempotentGetIsRetried", func(t *testing.T) {
		// The Echo backend kills the connection for the first 2 attempts;
		// fasthttp (divisor->backend) retries idempotent requests up to 5
		// times, so the client must transparently get a 200 on attempt 3.
		res := s.MustEcho(t, http.MethodGet, "/retry?fail_key=gk&fail_times=2", nil, nil)
		attempts := s.Backend(res.Echo.Backend).Counter(t, "gk")
		if attempts != 3 {
			t.Errorf("backend saw %d attempts, want 3 (2 failures + 1 success)", attempts)
		}
	})

	t.Run("FailedProxyAttemptReturns502", func(t *testing.T) {
		// SPEC (1.0, grilling Q13): a request divisor cannot complete
		// against its Backend returns 502 Bad Gateway
		// (internal/proxy/proxy.go serverError). fail_times=5 outlasts
		// every possible retry.
		res, err := s.Request(http.MethodPost, "/retry?fail_key=pk&fail_times=5", bytes.NewReader([]byte("x")), nil)
		if err != nil {
			t.Fatalf("request failed at transport level: %v", err)
		}
		attempts := s.Backend("a").Counter(t, "pk") + s.Backend("b").Counter(t, "pk")
		t.Logf("backend attempts for failing POST: %d", attempts)
		if res.StatusCode != http.StatusBadGateway {
			t.Errorf("Backend failure surfaced as %d, want 502 Bad Gateway (1.0 spec)", res.StatusCode)
		}
	})
}

// TestMaxRequestBodySizeConfigured proves an explicit (non-default)
// server.max_request_body_size reaches the server; the 4MB default is covered
// by the proxy matrix and the HTTP/2 suite.
func TestMaxRequestBodySizeConfigured(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:               "bodycap",
		Type:               "round-robin",
		MaxRequestBodySize: 128 << 10,
		Backends: []BackendSpec{
			{ID: "a"},
		},
	})

	t.Run("UnderLimit", func(t *testing.T) {
		body := testBody(64 << 10)
		res := s.MustEcho(t, http.MethodPost, "/undercap", body, nil)
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("64KB body arrived corrupted at backend")
		}
	})

	t.Run("OverLimit413", func(t *testing.T) {
		body := testBody(256 << 10)
		res, err := s.Request(http.MethodPost, "/overcap", bytes.NewReader(body), nil)
		if err != nil {
			t.Logf("256KB POST failed at transport level instead of a clean 413: %v", err)
			return
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("256KB POST got status %d, want 413 (configured 128KB cap)", res.StatusCode)
		}
		if res.Echo != nil {
			t.Errorf("256KB POST reached backend %s; the configured cap did not apply", res.Echo.Backend)
		}
	})

	t.Run("ChunkedAtLimit", func(t *testing.T) {
		// Exactly the cap must pass: the limit is inclusive on both paths.
		body := testBody(128 << 10)
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/chunkedatcap", rd, nil)
		if err != nil {
			t.Fatalf("chunked POST at the cap failed: %v", err)
		}
		if res.StatusCode != http.StatusOK || res.Echo == nil {
			t.Fatalf("chunked POST at the cap: status %d, body %.200s", res.StatusCode, res.Body)
		}
		if res.Echo.BodySha256 != sha256Hex(body) {
			t.Errorf("chunked body arrived corrupted at backend")
		}
	})

	t.Run("ChunkedOverLimit413", func(t *testing.T) {
		body := testBody(192 << 10)
		rd := struct{ io.Reader }{bytes.NewReader(body)}
		res, err := s.Request(http.MethodPost, "/chunkedovercap", rd, nil)
		if err != nil {
			t.Logf("oversized chunked POST failed at transport level instead of a clean 413: %v", err)
			return
		}
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized chunked POST got status %d, want 413 (configured 128KB cap)", res.StatusCode)
		}
		if res.Echo != nil {
			t.Errorf("oversized chunked POST reached backend %s; the configured cap did not apply", res.Echo.Backend)
		}
	})
}
