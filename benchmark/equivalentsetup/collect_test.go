package equivalentsetup

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/aaydin-tr/divisor/benchmark/backend"
	"github.com/aaydin-tr/divisor/benchmark/plan"
)

var sampled = plan.EquivalentSetupCheck{Requests: 400, Connections: 4}

func TestARoundRobinProxyOverKeepAliveConnectionsPassesEveryCheck(t *testing.T) {
	benchmarkBackends := startBenchmarkBackends(t)
	proxyURL := startRoundRobinProxy(t, benchmarkBackends, keepBackendConnectionsAlive)

	sample, err := Collect(context.Background(), proxyURL, sampled)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(sample.Answers) != sampled.Requests {
		t.Errorf("collected %d answers, want %d", len(sample.Answers), sampled.Requests)
	}
	if failedChecks := Check(sample, benchmarkBackends); len(failedChecks) > 0 {
		t.Errorf("failed checks:\n%s", strings.Join(failedChecks, "\n"))
	}
}

func TestOneBenchmarkBackendWithoutAProxyFailsTheSpreadAndXForwardedFor(t *testing.T) {
	benchmarkBackends := startBenchmarkBackends(t)

	sample, err := Collect(context.Background(), "http://"+benchmarkBackends[0].Address+"/", sampled)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	failedChecks := strings.Join(Check(sample, benchmarkBackends), "\n")
	for _, wantFailed := range []string{"an even spread over the 4 Benchmark backends", everyRequestHasForwardedFor} {
		if !strings.Contains(failedChecks, wantFailed) {
			t.Errorf("failed checks:\n%s\nwant %q among them", failedChecks, wantFailed)
		}
	}
}

func TestAProxyThatReconnectsForEveryRequestFailsKeepAlive(t *testing.T) {
	benchmarkBackends := startBenchmarkBackends(t)
	proxyURL := startRoundRobinProxy(t, benchmarkBackends, closeEveryBackendConnection)

	sample, err := Collect(context.Background(), proxyURL, sampled)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	failedChecks := strings.Join(Check(sample, benchmarkBackends), "\n")
	if !strings.Contains(failedChecks, keepAliveToBackends) {
		t.Errorf("failed checks:\n%s\nwant %q among them", failedChecks, keepAliveToBackends)
	}
}

func TestRequestsThatGetNoAnswerAreCountedNotFatal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddress := listener.Addr().String()
	_ = listener.Close()

	sample, err := Collect(context.Background(), "http://"+closedAddress+"/", plan.EquivalentSetupCheck{Requests: 4, Connections: 2})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	failedRequests := 0
	for _, count := range sample.Errors {
		failedRequests += count
	}
	if failedRequests != 4 || len(sample.Answers) != 0 {
		t.Errorf("sample = %+v, want 4 failed requests and no answers", sample)
	}
}

func startBenchmarkBackends(t *testing.T) []plan.BenchmarkBackend {
	t.Helper()
	benchmarkBackends := make([]plan.BenchmarkBackend, 0, len(benchmarkBackendNames))
	for _, name := range benchmarkBackendNames {
		benchmarkBackends = append(benchmarkBackends, plan.BenchmarkBackend{Name: name, Address: serve(t, backend.NewServer(name))})
	}
	return benchmarkBackends
}

type backendConnections bool

const (
	keepBackendConnectionsAlive backendConnections = true
	closeEveryBackendConnection backendConnections = false
)

func startRoundRobinProxy(t *testing.T, benchmarkBackends []plan.BenchmarkBackend, connections backendConnections) string {
	t.Helper()
	clients := make([]*fasthttp.HostClient, 0, len(benchmarkBackends))
	for _, benchmarkBackend := range benchmarkBackends {
		clients = append(clients, &fasthttp.HostClient{Addr: benchmarkBackend.Address})
	}
	var nextBackend atomic.Uint64
	proxy := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.Request.Header.Set(fasthttp.HeaderXForwardedFor, ctx.RemoteIP().String())
		if connections == closeEveryBackendConnection {
			ctx.Request.SetConnectionClose()
		}
		client := clients[(nextBackend.Add(1)-1)%uint64(len(clients))]
		if err := client.Do(&ctx.Request, &ctx.Response); err != nil {
			ctx.Error(err.Error(), fasthttp.StatusBadGateway)
		}
	}}
	return "http://" + serve(t, proxy) + "/"
}

func serve(t *testing.T, server *fasthttp.Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return listener.Addr().String()
}
