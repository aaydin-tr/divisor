package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Kubelet Probes and Prometheus scrapes at peak traffic: /healthz, /ready and /metrics answer within 1s, kubelet's
// default Probe timeout, while the load keeps divisor's core busy.
func TestOneCPUMonitoringAnswersWithinOneSecondUnderLoad(t *testing.T) {
	t.Parallel()
	const kubeletProbeTimeout = time.Second
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-monitoring",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, func(t *testing.T, s *Scenario) {
		load := startBurstLoad(t, s, 32, 50*time.Millisecond, sameEchoRequest("/monitored"))
		var slowOrFailed []string
		probedUntil := time.Now().Add(4 * time.Second)
		for time.Now().Before(probedUntil) {
			for _, path := range []string{"/healthz", "/ready", "/metrics"} {
				start := time.Now()
				_, _, err := getMonitoringOK(s, path)
				if elapsed := time.Since(start); err != nil || elapsed > kubeletProbeTimeout {
					slowOrFailed = append(slowOrFailed, fmt.Sprintf("%s after %s: %v", path, elapsed, err))
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		outcomes := load.stop()
		logLoadSummary(t, outcomes)
		requireEveryRequestEchoed(t, outcomes)
		if len(slowOrFailed) > 0 {
			t.Errorf("%d monitoring requests failed or took over %s under load; first ones: %v",
				len(slowOrFailed), kubeletProbeTimeout, slowOrFailed[:min(5, len(slowOrFailed))])
		}
	})
}

// SSE or LLM token streams next to normal API calls: frames still arrive as the Backend sends them, and a client
// disconnect still closes the Backend stream, while batched answers flow around them.
func TestOneCPUStreamedResponsesNextToBatchedTraffic(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:      "rb-stream",
		Type:      "round-robin",
		Streaming: true,
		Backends:  []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, func(t *testing.T, s *Scenario) {
		load := startBurstLoad(t, s, 32, 100*time.Millisecond, sameEchoRequest("/next-to-streams"))
		requireFramesArriveIncrementally(t, s)
		requireClientDisconnectClosesBackendStream(t, s)
		outcomes := load.stop()
		logLoadSummary(t, outcomes)
		requireEveryRequestEchoed(t, outcomes)
	})
}

func requireFramesArriveIncrementally(t *testing.T, s *Scenario) {
	t.Helper()
	const frameCount = 5
	start := time.Now()
	resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=" + strconv.Itoa(frameCount) + "&interval=" + streamFrameInterval.String())
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream got status %d, want 200", resp.StatusCode)
	}
	frames := bufio.NewReader(resp.Body)
	arrivals := make([]time.Duration, 0, frameCount)
	for i := range frameCount {
		frame, err := readEventFrame(frames)
		if err != nil {
			t.Fatalf("reading frame %d: %v", i, err)
		}
		if want := "data: " + strconv.Itoa(i) + "\n\n"; frame != want {
			t.Fatalf("frame %d = %q, want %q", i, frame, want)
		}
		arrivals = append(arrivals, time.Since(start))
	}
	// A body held back until its end would bring the first frame after (frameCount-1) intervals.
	if arrivals[0] > 2*streamFrameInterval {
		t.Errorf("first frame arrived after %s; a streamed frame must not wait for the rest of the body", arrivals[0])
	}
	for i := 1; i < frameCount; i++ {
		if gap := arrivals[i] - arrivals[i-1]; gap < streamFrameInterval/2 {
			t.Errorf("frame %d arrived %s after frame %d; frames must arrive as the Backend sends them (arrivals: %v)", i, gap, i-1, arrivals)
		}
	}
}

func requireClientDisconnectClosesBackendStream(t *testing.T, s *Scenario) {
	t.Helper()
	const promptly = 5 * time.Second
	clientGoneKey := newRequestID()
	resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=2&interval=60s&client_gone_key=" + clientGoneKey)
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	streamingBackend := s.Backend(resp.Header.Get("X-Backend-Id"))
	if streamingBackend == nil {
		resp.Body.Close()
		t.Fatalf("the stream's answer names no known Backend: %q", resp.Header.Get("X-Backend-Id"))
	}
	if frame, err := readEventFrame(bufio.NewReader(resp.Body)); err != nil || frame != "data: 0\n\n" {
		resp.Body.Close()
		t.Fatalf("first frame = %q, err %v; want data: 0", frame, err)
	}

	resp.Body.Close()
	disconnectedAt := time.Now()
	for streamingBackend.Counter(t, clientGoneKey) == 0 {
		if time.Since(disconnectedAt) > promptly {
			t.Fatalf("Backend %s's stream was still open %s after its client disconnected", streamingBackend.ID, promptly)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A rolling update at peak traffic: the load keeps sending through SIGTERM, every request that reached a Backend
// gets its answer (none is cut), /ready flips to 503, and divisor exits 0.
func TestOneCPUGracefulShutdownUnderLoad(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-shutdown",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, func(t *testing.T, s *Scenario) {
		// Each answer takes longer than the burst interval, so every load client has a request Pending at SIGTERM.
		load := startBurstLoad(t, s, 32, 100*time.Millisecond, sameEchoRequest("/draining?delay=1s"))
		time.Sleep(time.Second)

		// Slow requests keep the drain open long enough to see /ready flip.
		const slowRequestCount = 16
		slowRequestIDs := make([]string, slowRequestCount)
		slowOutcomes := make([]requestOutcome, slowRequestCount)
		var slowRequestsDone sync.WaitGroup
		for slowIndex := range slowRequestCount {
			slowRequestIDs[slowIndex] = newRequestID()
			slowRequestsDone.Add(1)
			go func() {
				defer slowRequestsDone.Done()
				client := newSingleConnectionClient(s)
				defer client.CloseIdleConnections()
				slowOutcomes[slowIndex] = sendIdentifiedRequest(client, s, slowRequestIDs[slowIndex], slowIndex, 0, sameEchoRequest("/pending-at-sigterm?delay=3s"))
			}()
		}
		eventually(t, 10*time.Second, "every slow request reached a Backend", func() error {
			for _, requestID := range slowRequestIDs {
				if backendsCounted(t, s, requestID) == 0 {
					return fmt.Errorf("slow request %s has not reached a Backend yet", requestID)
				}
			}
			return nil
		})

		terminationBegan := time.Now()
		s.TerminateDivisor(t)
		terminatedAt := time.Now()
		eventually(t, 5*time.Second, "/ready flipped to 503 when graceful shutdown began", func() error {
			if code, err := monitoringStatus(s, "/ready"); err != nil || code != http.StatusServiceUnavailable {
				return fmt.Errorf("/ready answered %d (%v), want 503", code, err)
			}
			return nil
		})
		slowRequestsDone.Wait()
		outcomes := load.stop()
		logLoadSummary(t, outcomes)
		var answeredAfterSIGTERM int
		for _, o := range outcomes {
			if o.sentAt.Add(o.latency).After(terminatedAt) {
				answeredAfterSIGTERM++
			}
		}
		t.Logf("%d load requests were answered after SIGTERM, besides the %d slow ones", answeredAfterSIGTERM, slowRequestCount)
		if code := s.WaitDivisorExit(t, 35*time.Second); code != 0 {
			t.Errorf("divisor exited with code %d after SIGTERM, want 0", code)
		}
		requireEveryRequestEchoed(t, slowOutcomes)
		// A load client's request after the drain finds its connection closed and the listener gone.
		requireEveryRequestEchoedExcept(t, outcomes, func(o requestOutcome) bool {
			return o.noAnswerArrived() && !o.sentAt.Before(terminationBegan) && backendsCounted(t, s, o.requestID) == 0
		})
	})
}

// backendsCounted is how many times the Backends saw the identified request.
func backendsCounted(t *testing.T, s *Scenario, requestID string) uint64 {
	t.Helper()
	var seen uint64
	for _, e := range s.Backends {
		seen += e.Counter(t, requestID)
	}
	return seen
}

// apiKeyMW guards /guarded: it answers a request there without the configured API key itself, with a 401 naming
// that request.
const apiKeyMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type APIKey struct {
	key string
}

func (m *APIKey) OnRequest(ctx *middleware.Context) error {
	if string(ctx.Path()) != "/guarded" || string(ctx.Request.Header.Peek("X-Api-Key")) == m.key {
		return nil
	}
	requestID := string(ctx.Request.Header.Peek("X-Request-Id"))
	ctx.SetStatusCode(401)
	ctx.Response.Header.Set("X-Request-Id", requestID)
	ctx.SetBodyString("missing API key for " + requestID)
	return middleware.ErrShortCircuit
}

func (m *APIKey) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	key, _ := config["key"].(string)
	return &APIKey{key: key}
}
`

// Auth or rate-limit Middleware answering without a Backend: under concurrency each request gets its own answer,
// the Middleware's 401 for one without the API key, the Echo backend's 200 for one with it.
func TestOneCPUMiddlewareAnswersUnderLoad(t *testing.T) {
	t.Parallel()
	const apiKey = "integration-api-key"
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:        "rb-middleware",
		Type:        "round-robin",
		Backends:    []BackendSpec{{ID: "a"}, {ID: "b"}},
		Middlewares: []MiddlewareSpec{{Name: "api-key", Code: apiKeyMW, Config: map[string]any{"key": apiKey}}},
	}, func(t *testing.T, s *Scenario) {
		withAPIKeyEveryOtherRequest := func(_ string, requestIndex int) (string, http.Header) {
			if requestIndex%2 == 0 {
				return "/guarded", http.Header{"X-Api-Key": {apiKey}}
			}
			return "/guarded", nil
		}
		outcomes := runConcurrentClients(s, 48, 30, withAPIKeyEveryOtherRequest)
		logLoadSummary(t, outcomes)

		var wrong []requestOutcome
		for _, o := range outcomes {
			hasAPIKey := o.requestIndex%2 == 0
			middlewareAnswer := o.status == http.StatusUnauthorized && o.backend == "" &&
				o.answerRequestID == o.requestID && o.ownAnswerBody == "missing API key for "+o.requestID
			if (hasAPIKey && !o.echoedOK()) || (!hasAPIKey && !middlewareAnswer) {
				wrong = append(wrong, o)
			}
		}
		if len(wrong) > 0 {
			t.Errorf("%d of %d requests got an answer that is not theirs (the Echo backend's for a key, the Middleware's 401 without); first ones: %s",
				len(wrong), len(outcomes), describeFirstOutcomes(wrong))
		}
	})
}

// Log pipelines that count requests: one Access log line per answered request, each with the status that request
// got and a duration above 0.
func TestOneCPUAccessLogUnderLoad(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:      "rb-accesslog",
		Type:      "round-robin",
		AccessLog: true,
		Backends:  []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, func(t *testing.T, s *Scenario) {
		statuses := []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError}
		// The path names the request, since the Access log logs the path without its query.
		loggedPathWithStatus := func(requestID string, requestIndex int) (string, http.Header) {
			return "/logged/" + requestID + "?status=" + strconv.Itoa(statuses[requestIndex%len(statuses)]), nil
		}
		outcomes := runConcurrentClients(s, 32, 25, loggedPathWithStatus)
		logLoadSummary(t, outcomes)

		wantStatusByPath := map[string]int{}
		for _, o := range outcomes {
			want := statuses[o.requestIndex%len(statuses)]
			if o.status != want || o.backend == "" || o.err != nil {
				t.Fatalf("request %v, want status %d from an Echo backend", o, want)
			}
			wantStatusByPath["/logged/"+o.requestID] = want
		}

		var linesByPath map[string][]map[string]any
		eventually(t, 15*time.Second, "an Access log line for every request arrives on stdout", func() error {
			linesByPath = accessLogLinesByPath(t, s, "/logged/")
			if len(linesByPath) < len(wantStatusByPath) {
				return fmt.Errorf("Access log lines for %d of %d requests", len(linesByPath), len(wantStatusByPath))
			}
			return nil
		})
		var wrong []string
		for path, lines := range linesByPath {
			wantStatus, sent := wantStatusByPath[path]
			switch {
			case !sent:
				wrong = append(wrong, fmt.Sprintf("a line for %s, which no request asked for", path))
			case len(lines) != 1:
				wrong = append(wrong, fmt.Sprintf("%d lines for %s, want 1", len(lines), path))
			case lines[0]["status"] != float64(wantStatus):
				wrong = append(wrong, fmt.Sprintf("%s logged status %v, want %d", path, lines[0]["status"], wantStatus))
			default:
				if duration, _ := lines[0]["duration_ms"].(float64); duration <= 0 {
					wrong = append(wrong, fmt.Sprintf("%s logged duration_ms %v, want above 0", path, lines[0]["duration_ms"]))
				}
			}
		}
		if len(wrong) > 0 {
			t.Errorf("%d Access log mismatches; first ones: %v", len(wrong), wrong[:min(5, len(wrong))])
		}
	})
}

func accessLogLinesByPath(t *testing.T, s *Scenario, pathPrefix string) map[string][]map[string]any {
	t.Helper()
	stdout, _ := divisorStreams(t, s.Divisor)
	linesByPath := map[string][]map[string]any{}
	for _, line := range nonBlankLines(stdout) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Access log line is not JSON: %q", line)
		}
		if path, _ := entry["path"].(string); strings.HasPrefix(path, pathPrefix) {
			linesByPath[path] = append(linesByPath[path], entry)
		}
	}
	return linesByPath
}
