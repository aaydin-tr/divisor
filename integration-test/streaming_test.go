package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// streamFrameInterval is far above container scheduling noise, so arrival
// times tell a forwarded frame from one held back with the rest of the body.
const streamFrameInterval = time.Second

// Every streaming Scenario runs on both client-facing stacks: fasthttp for
// HTTP/1.1, and net/http behind the adapter for HTTP/2 (TLS).
var listenerStacks = []struct {
	name       string
	http2      bool
	protoMajor int
}{
	{"h1", false, 1},
	{"h2", true, 2},
}

// SSE with server.streaming on: each frame reaches the client as the Echo backend flushes it, and the stream
// outlives proxy_timeout, which bounds only the first response byte.
func TestEventStreamFramesArriveIncrementally(t *testing.T) {
	t.Parallel()
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()
			s := startScenario(t, ScenarioSpec{
				Name:         "stream-sse-" + stack.name,
				HTTP2:        stack.http2,
				Streaming:    true,
				ProxyTimeout: 2 * time.Second,
				Backends:     []BackendSpec{{ID: "a"}},
			})

			const frameCount = 4
			start := time.Now()
			resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=" + strconv.Itoa(frameCount) + "&interval=" + streamFrameInterval.String())
			if err != nil {
				t.Fatalf("stream request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.ProtoMajor != stack.protoMajor {
				t.Fatalf("stream negotiated %s, want HTTP/%d", resp.Proto, stack.protoMajor)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stream got status %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
				t.Errorf("stream Content-Type = %q, want text/event-stream", got)
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
			if rest, err := io.ReadAll(frames); err != nil || len(rest) != 0 {
				t.Errorf("stream end: read %q, err %v; want a clean end after the last frame", rest, err)
			}

			if arrivals[0] > streamFrameInterval {
				t.Errorf("first frame arrived after %s; a streamed frame must not wait for the rest of the body", arrivals[0])
			}
			for i := 1; i < frameCount; i++ {
				if gap := arrivals[i] - arrivals[i-1]; gap < streamFrameInterval/2 {
					t.Errorf("frame %d arrived %s after frame %d; frames must arrive as the backend sends them (arrivals: %v)", i, gap, i-1, arrivals)
				}
			}
		})
	}
}

// With server.streaming off (the default), an event stream that outlasts
// proxy_timeout is still today's bounded failure: a 504 at proxy_timeout.
func TestEventStreamWithStreamingOffGets504AtProxyTimeout(t *testing.T) {
	t.Parallel()
	const proxyTimeout = 2 * time.Second
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()
			s := startScenario(t, ScenarioSpec{
				Name:         "stream-off-" + stack.name,
				HTTP2:        stack.http2,
				ProxyTimeout: proxyTimeout,
				Backends:     []BackendSpec{{ID: "a"}},
			})

			start := time.Now()
			res, err := s.Request(http.MethodGet, "/stream?frames=10&interval="+streamFrameInterval.String(), nil, nil)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("stream request failed at transport level: %v", err)
			}
			if res.StatusCode != http.StatusGatewayTimeout {
				t.Errorf("stream with streaming off got status %d, want 504", res.StatusCode)
			}
			if body := strings.TrimSpace(string(res.Body)); body != `{"message":"gateway timeout"}` {
				t.Errorf("stream with streaming off got body %q, want the gateway timeout message", body)
			}
			// Loose bounds: the container's timers are measured from the host.
			// The stream itself would last 9s.
			if elapsed < proxyTimeout/2 || elapsed > proxyTimeout+3*time.Second {
				t.Errorf("504 arrived after %s, want about proxy_timeout (%s)", elapsed, proxyTimeout)
			}
		})
	}
}

// A stream with no data flowing for stream_idle_timeout is cut mid-body, never ended cleanly (HTTP/1.1: the
// connection closes; HTTP/2: the stream is reset).
func TestSilentEventStreamEndsAtIdleTimeout(t *testing.T) {
	t.Parallel()
	const idleTimeout = 2 * time.Second
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()
			s := startScenario(t, ScenarioSpec{
				Name:              "stream-idle-" + stack.name,
				HTTP2:             stack.http2,
				Streaming:         true,
				StreamIdleTimeout: idleTimeout,
				Backends:          []BackendSpec{{ID: "a"}},
			})

			resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=2&interval=20s")
			if err != nil {
				t.Fatalf("stream request failed: %v", err)
			}
			defer resp.Body.Close()
			frames := bufio.NewReader(resp.Body)
			if frame, err := readEventFrame(frames); err != nil || frame != "data: 0\n\n" {
				t.Fatalf("first frame = %q, err %v; want data: 0", frame, err)
			}

			silentSince := time.Now()
			rest, err := io.ReadAll(frames)
			silentFor := time.Since(silentSince)
			if err == nil {
				t.Errorf("stream ended cleanly after %q; an idle-reaped stream must end mid-body", rest)
			}
			if silentFor < idleTimeout/2 || silentFor > idleTimeout+5*time.Second {
				t.Errorf("silent stream ended after %s, want about stream_idle_timeout (%s)", silentFor, idleTimeout)
			}
		})
	}
}

// A client that goes away mid-stream takes its Backend stream with it, long before the next frame (or
// stream_idle_timeout) is due.
func TestClientDisconnectClosesBackendStream(t *testing.T) {
	t.Parallel()
	const promptly = 5 * time.Second
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()
			s := startScenario(t, ScenarioSpec{
				Name:      "stream-disconnect-" + stack.name,
				HTTP2:     stack.http2,
				Streaming: true,
				Backends:  []BackendSpec{{ID: "a"}},
			})

			const clientGoneKey = "stream-disconnect"
			resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=2&interval=60s&client_gone_key=" + clientGoneKey)
			if err != nil {
				t.Fatalf("stream request failed: %v", err)
			}
			if resp.ProtoMajor != stack.protoMajor {
				t.Fatalf("stream negotiated %s, want HTTP/%d", resp.Proto, stack.protoMajor)
			}
			if frame, err := readEventFrame(bufio.NewReader(resp.Body)); err != nil || frame != "data: 0\n\n" {
				t.Fatalf("first frame = %q, err %v; want data: 0", frame, err)
			}

			resp.Body.Close()
			disconnectedAt := time.Now()
			for s.Backend("a").Counter(t, clientGoneKey) == 0 {
				if time.Since(disconnectedAt) > promptly {
					t.Fatalf("the Echo backend's stream was still open %s after its client disconnected", promptly)
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// A Streamed response is answered when its stream ends, so its Access log
// line arrives then, covering the whole stream and every byte forwarded.
func TestStreamedRequestIsAccessLoggedAtStreamEnd(t *testing.T) {
	t.Parallel()
	const frameCount = 3
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			t.Parallel()
			s := startScenario(t, ScenarioSpec{
				Name:      "stream-accesslog-" + stack.name,
				HTTP2:     stack.http2,
				Streaming: true,
				AccessLog: true,
				Backends:  []BackendSpec{{ID: "a"}},
			})

			resp, err := s.NewClient(30 * time.Second).Get(s.BaseURL + "/stream?frames=" + strconv.Itoa(frameCount) + "&interval=" + streamFrameInterval.String())
			if err != nil {
				t.Fatalf("stream request failed: %v", err)
			}
			defer resp.Body.Close()
			frames := bufio.NewReader(resp.Body)
			firstFrame, err := readEventFrame(frames)
			if err != nil || firstFrame != "data: 0\n\n" {
				t.Fatalf("first frame = %q, err %v; want data: 0", firstFrame, err)
			}
			if lines := accessLogLinesForStreamRequests(t, s); len(lines) != 0 {
				t.Errorf("an Access log line arrived while the stream was open: %v", lines)
			}
			rest, err := io.ReadAll(frames)
			if err != nil {
				t.Fatalf("reading the rest of the stream: %v", err)
			}

			var line map[string]any
			eventually(t, 10*time.Second, "the stream's Access log line arrives on stdout", func() error {
				lines := accessLogLinesForStreamRequests(t, s)
				if len(lines) != 1 {
					return fmt.Errorf("got %d Access log lines for /stream, want 1", len(lines))
				}
				line = lines[0]
				return nil
			})
			if line["status"] != float64(http.StatusOK) {
				t.Errorf("status = %v, want 200", line["status"])
			}
			if want := float64(len(firstFrame) + len(rest)); line["bytes_out"] != want {
				t.Errorf("bytes_out = %v, want the %v bytes the client received", line["bytes_out"], want)
			}
			// Loose bound: the stream lasts (frameCount-1) intervals.
			if duration, _ := line["duration_ms"].(float64); duration < float64((frameCount-1)*streamFrameInterval.Milliseconds())/2 {
				t.Errorf("duration_ms = %v, want the whole stream (about %s)", duration, (frameCount-1)*streamFrameInterval)
			}
		})
	}
}

func accessLogLinesForStreamRequests(t *testing.T, s *Scenario) []map[string]any {
	t.Helper()
	stdout, _ := divisorStreams(t, s.Divisor)
	var lines []map[string]any
	for _, line := range nonBlankLines(stdout) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("access log line is not JSON: %q", line)
		}
		if entry["path"] == "/stream" {
			lines = append(lines, entry)
		}
	}
	return lines
}

func readEventFrame(r *bufio.Reader) (string, error) {
	var frame strings.Builder
	for {
		line, err := r.ReadString('\n')
		frame.WriteString(line)
		if err != nil {
			return frame.String(), err
		}
		if line == "\n" {
			return frame.String(), nil
		}
	}
}
