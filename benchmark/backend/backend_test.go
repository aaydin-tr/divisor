package backend

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func startBenchmarkBackend(t *testing.T, name string) *fasthttp.HostClient {
	t.Helper()
	listener := fasthttputil.NewInmemoryListener()
	server := NewServer(name)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return &fasthttp.HostClient{
		Addr:     "benchmark-backend",
		MaxConns: 1,
		Dial:     func(string) (net.Conn, error) { return listener.Dial() },
	}
}

func get(t *testing.T, client *fasthttp.HostClient, path string, headers map[string]string) *fasthttp.Response {
	t.Helper()
	request := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(request)
	request.SetRequestURI("http://benchmark-backend" + path)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := &fasthttp.Response{}
	if err := client.Do(request, response); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return response
}

func TestEveryPathGetsTheSameFixedAnswer(t *testing.T) {
	client := startBenchmarkBackend(t, "backend-1")

	probe := get(t, client, "/", nil)
	request := get(t, client, "/any/path?with=query", nil)

	for _, response := range []*fasthttp.Response{probe, request} {
		if response.StatusCode() != fasthttp.StatusOK {
			t.Errorf("status = %d, want 200", response.StatusCode())
		}
		if bodySize := len(response.Body()); bodySize < 900 || bodySize > 1100 {
			t.Errorf("body is %d bytes, want about 1 KB", bodySize)
		}
	}
	if string(probe.Body()) != string(request.Body()) {
		t.Error("the Probe and the request got different bodies")
	}
}

func TestAnswerNamesTheBackendAndTheConnection(t *testing.T) {
	client := startBenchmarkBackend(t, "backend-3")

	first := string(get(t, client, "/", nil).Header.Peek(AnsweredByHeader))
	second := string(get(t, client, "/other", nil).Header.Peek(AnsweredByHeader))

	backendName, connectionID, found := strings.Cut(first, "/")
	if !found || backendName != "backend-3" {
		t.Fatalf("%s = %q, want backend-3/<connection id>", AnsweredByHeader, first)
	}
	if _, err := strconv.ParseUint(connectionID, 10, 64); err != nil {
		t.Fatalf("%s = %q, connection id is not a number", AnsweredByHeader, first)
	}
	if first != second {
		t.Errorf("two requests on one keep-alive connection answered %q and %q, want the same connection id", first, second)
	}
}

func TestAnswerSaysWhetherXForwardedForArrived(t *testing.T) {
	client := startBenchmarkBackend(t, "backend-1")

	forwarded := get(t, client, "/", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	direct := get(t, client, "/", nil)

	if got := string(forwarded.Header.Peek(ForwardedForHeader)); got != "present" {
		t.Errorf("with X-Forwarded-For: %s = %q, want present", ForwardedForHeader, got)
	}
	if got := string(direct.Header.Peek(ForwardedForHeader)); got != "absent" {
		t.Errorf("without X-Forwarded-For: %s = %q, want absent", ForwardedForHeader, got)
	}
}
