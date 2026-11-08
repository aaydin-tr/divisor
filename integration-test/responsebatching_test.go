package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Response batching: on 2 CPUs or fewer, divisor yields before writing a response that is not streamed. The
// scenarios in responsebatching*_test.go check the yield changes nothing a client, a Backend or an operator sees.
var responseBatchingModes = []struct {
	name             string
	scenarioSuffix   string
	batchingDisabled bool
}{
	{name: "batching-on", scenarioSuffix: "on", batchingDisabled: false},
	{name: "batching-off", scenarioSuffix: "off", batchingDisabled: true},
}

// onOneCPUWithBatchingOnAndOff runs scenarioBody on two divisors limited to one CPU, one with response batching on
// (the default) and one with server.disable_response_batching: both must pass the same assertions.
func onOneCPUWithBatchingOnAndOff(t *testing.T, spec ScenarioSpec, scenarioBody func(t *testing.T, s *Scenario)) {
	t.Helper()
	onCPUsWithBatchingOnAndOff(t, 1, spec, scenarioBody)
}

func onCPUsWithBatchingOnAndOff(t *testing.T, cpuLimit int, spec ScenarioSpec, scenarioBody func(t *testing.T, s *Scenario)) {
	t.Helper()
	for _, mode := range responseBatchingModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			modeSpec := spec
			modeSpec.Name = spec.Name + "-" + mode.scenarioSuffix
			modeSpec.Backends = slices.Clone(spec.Backends)
			modeSpec.CPULimit = cpuLimit
			modeSpec.DisableResponseBatching = mode.batchingDisabled
			modeSpec.ExposeMonitoring = true
			s := startScenario(t, modeSpec)
			requireGOMAXPROCS(t, s, cpuLimit)
			scenarioBody(t, s)
		})
	}
}

// requireGOMAXPROCS reads the GOMAXPROCS divisor's Go runtime derived from its CPU limit, as /metrics reports it.
func requireGOMAXPROCS(t *testing.T, s *Scenario, want int) {
	t.Helper()
	var gomaxprocs float64
	eventually(t, 10*time.Second, "/metrics reports divisor's GOMAXPROCS", func() error {
		samples, err := scrapeMetrics(s)
		if err != nil {
			return err
		}
		reported, ok := samples["go_sched_gomaxprocs_threads"]
		if !ok {
			return fmt.Errorf("/metrics has no go_sched_gomaxprocs_threads")
		}
		gomaxprocs = reported
		return nil
	})
	if gomaxprocs != float64(want) {
		t.Fatalf("divisor runs with GOMAXPROCS %v, want %d from its CPU limit", gomaxprocs, want)
	}
}

func divisorAddress(s *Scenario) string { return strings.TrimPrefix(s.BaseURL, "http://") }

// Many clients on keep-alive connections at once: each gets the answer to its own request, every one a 200, each
// client keeps its one connection, and round-robin spreads the requests evenly.
func TestOneCPUKeepAliveClientsGetTheirOwnResponses(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-keepalive",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}, requireKeepAliveClientsGetTheirOwnResponses)
}

// Two CPUs is the most divisor batches on: the GOMAXPROCS Go gives a Kubernetes limits.cpu of 2 or less.
func TestTwoCPUsKeepAliveClientsGetTheirOwnResponses(t *testing.T) {
	t.Parallel()
	onCPUsWithBatchingOnAndOff(t, 2, ScenarioSpec{
		Name:     "rb-twocpus-keepalive",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}, requireKeepAliveClientsGetTheirOwnResponses)
}

// On three CPUs divisor never batches; the same keep-alive traffic must be served all the same.
func TestThreeCPUsKeepAliveClientsGetTheirOwnResponses(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:             "rb-threecpus",
		Type:             "round-robin",
		CPULimit:         3,
		ExposeMonitoring: true,
		Backends:         []BackendSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	})
	requireGOMAXPROCS(t, s, 3)
	requireKeepAliveClientsGetTheirOwnResponses(t, s)
}

func requireKeepAliveClientsGetTheirOwnResponses(t *testing.T, s *Scenario) {
	t.Helper()
	const clientCount, requestsPerClient = 64, 30
	outcomes := runConcurrentClients(s, clientCount, requestsPerClient, sameEchoRequest("/keepalive"))
	logLoadSummary(t, outcomes)
	requireEveryRequestEchoed(t, outcomes)

	connectionsOpened := make([]int, clientCount)
	for _, o := range outcomes {
		if o.openedConnection {
			connectionsOpened[o.clientIndex]++
		}
	}
	for clientIndex, opened := range connectionsOpened {
		if opened != 1 {
			t.Errorf("client %d opened %d connections for its %d requests, want its one keep-alive connection (per client: %v)",
				clientIndex, opened, requestsPerClient, connectionsOpened)
			break
		}
	}
	requireEvenSpread(t, countByBackend(outcomes), "a", "b", "c")
}

// A client, or a proxy in front, that pipelines: every request of the pipeline goes out before any answer is read,
// and the answers come back in request order, each the answer to the request at its position.
func TestOneCPUPipelinedRequestsAreAnsweredInOrder(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-pipeline",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, requirePipelinedRequestsAnsweredInOrder)
}

func TestTwoCPUsPipelinedRequestsAreAnsweredInOrder(t *testing.T) {
	t.Parallel()
	onCPUsWithBatchingOnAndOff(t, 2, ScenarioSpec{
		Name:     "rb-twocpus-pipeline",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, requirePipelinedRequestsAnsweredInOrder)
}

func requirePipelinedRequestsAnsweredInOrder(t *testing.T, s *Scenario) {
	t.Helper()
	// Several pipelining connections at once, so divisor has other requests to run when it yields.
	const connectionCount, pipelinesPerConnection, pipelineDepth = 16, 3, 20
	failures := make([]error, connectionCount)
	var connectionsDone sync.WaitGroup
	for connectionIndex := range connectionCount {
		connectionsDone.Add(1)
		go func() {
			defer connectionsDone.Done()
			failures[connectionIndex] = pipelineOnOneConnection(divisorAddress(s), pipelinesPerConnection, pipelineDepth)
		}()
	}
	connectionsDone.Wait()
	for connectionIndex, err := range failures {
		if err != nil {
			t.Errorf("pipelining connection %d: %v", connectionIndex, err)
		}
	}
}

func pipelineOnOneConnection(address string, pipelineCount, pipelineDepth int) error {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	answers := bufio.NewReader(conn)
	for range pipelineCount {
		requestIDs := make([]string, pipelineDepth)
		var pipeline bytes.Buffer
		for position := range requestIDs {
			requestIDs[position] = newRequestID()
			target := withIdentifyingQuery("/pipelined", requestIDs[position])
			if position%5 == 0 {
				target += "&delay=20ms" // a slow answer early in the pipeline must not be overtaken
			}
			fmt.Fprintf(&pipeline, "GET %s HTTP/1.1\r\nHost: divisor\r\n%s: %s\r\n\r\n", target, requestIDHeader, requestIDs[position])
		}
		if _, err := conn.Write(pipeline.Bytes()); err != nil {
			return fmt.Errorf("writing %d pipelined requests: %w", pipelineDepth, err)
		}
		for position, requestID := range requestIDs {
			if err := readPipelinedAnswer(answers, requestID); err != nil {
				return fmt.Errorf("answer %d of %d: %w", position+1, pipelineDepth, err)
			}
		}
	}
	return nil
}

func readPipelinedAnswer(answers *bufio.Reader, requestID string) error {
	resp, err := http.ReadResponse(answers, nil)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d, body %.200s", resp.StatusCode, body)
	}
	var echo EchoResp
	if err := json.Unmarshal(body, &echo); err != nil {
		return fmt.Errorf("not an echo reply: %.200s", body)
	}
	if echoed, answered := echo.Header(requestIDHeader), resp.Header.Get(requestIDHeader); echoed != requestID || answered != requestID {
		return fmt.Errorf("it answers request %q (header %q), want %q: the answers left request order", echoed, answered, requestID)
	}
	return nil
}

// File downloads and big JSON under concurrency: every 1 MB to 4 MB answer arrives whole, byte for byte the body its
// Backend sent for that request.
func TestOneCPULargeResponsesArriveIntact(t *testing.T) {
	t.Parallel()
	onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
		Name:     "rb-large",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
	}, func(t *testing.T, s *Scenario) {
		const clientCount, downloadsPerClient = 16, 4
		sizes := []int{1 << 20, 2<<20 + 1, 3<<20 + 4093, 4 << 20}
		failures := make([][]string, clientCount)
		var clientsDone sync.WaitGroup
		for clientIndex := range clientCount {
			clientsDone.Add(1)
			go func() {
				defer clientsDone.Done()
				client := newSingleConnectionClient(s)
				defer client.CloseIdleConnections()
				for download := range downloadsPerClient {
					if err := downloadBytes(client, s, sizes[(clientIndex+download)%len(sizes)]); err != nil {
						failures[clientIndex] = append(failures[clientIndex], err.Error())
					}
				}
			}()
		}
		clientsDone.Wait()
		if failed := slices.Concat(failures...); len(failed) > 0 {
			t.Fatalf("%d of %d large answers did not arrive intact; first ones: %v", len(failed), clientCount*downloadsPerClient, failed[:min(5, len(failed))])
		}
	})
}

// downloadBytes fetches size bytes from the Echo backend's /bytes under a seed no other request uses, so another
// request's body fails the seed check and a cut or corrupted one fails the checksum.
func downloadBytes(client *http.Client, s *Scenario, size int) error {
	seed := newBodySeed()
	resp, err := client.Get(fmt.Sprintf("%s/bytes?size=%d&seed=%d", s.BaseURL, size, seed))
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("%d-byte answer of seed %d: reading the body: %w", size, seed, err)
	}
	return verifyBytesAnswer(resp, body, size, seed)
}

func verifyBytesAnswer(resp *http.Response, body []byte, size int, seed uint64) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%d-byte answer of seed %d: status %d", size, seed, resp.StatusCode)
	}
	if answered := resp.Header.Get("X-Body-Seed"); answered != strconv.FormatUint(seed, 10) {
		return fmt.Errorf("asked for the body of seed %d, got the one of seed %q", seed, answered)
	}
	if len(body) != size || resp.ContentLength != int64(size) {
		return fmt.Errorf("%d-byte answer of seed %d arrived with %d bytes, Content-Length %d", size, seed, len(body), resp.ContentLength)
	}
	if sha256Hex(body) != resp.Header.Get("X-Body-Sha256") {
		return fmt.Errorf("%d-byte answer of seed %d arrived corrupted: its checksum is not the one its Backend sent", size, seed)
	}
	return nil
}

// Connections closed after one response, by a client sending Connection: close or by divisor's
// server.disable_keepalive: each answer arrives whole before the connection closes.
func TestOneCPUClosedConnectionsGetCompleteResponses(t *testing.T) {
	t.Parallel()
	t.Run("client-sends-connection-close", func(t *testing.T) {
		t.Parallel()
		onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
			Name:     "rb-connclose",
			Type:     "round-robin",
			Backends: []BackendSpec{{ID: "a"}, {ID: "b"}},
		}, func(t *testing.T, s *Scenario) {
			requireWholeAnswersBeforeClose(t, s, http.Header{"Connection": {"close"}})
		})
	})
	t.Run("divisor-disables-keepalive", func(t *testing.T) {
		t.Parallel()
		onOneCPUWithBatchingOnAndOff(t, ScenarioSpec{
			Name:             "rb-nokeepalive",
			Type:             "round-robin",
			DisableKeepalive: true,
			Backends:         []BackendSpec{{ID: "a"}, {ID: "b"}},
		}, func(t *testing.T, s *Scenario) {
			requireWholeAnswersBeforeClose(t, s, nil)
		})
	})
}

func requireWholeAnswersBeforeClose(t *testing.T, s *Scenario, requestHeader http.Header) {
	t.Helper()
	const clientCount, requestsPerClient = 32, 16
	failures := make([][]string, clientCount)
	var clientsDone sync.WaitGroup
	for clientIndex := range clientCount {
		clientsDone.Add(1)
		go func() {
			defer clientsDone.Done()
			for requestIndex := range requestsPerClient {
				var err error
				if requestIndex%2 == 0 {
					err = echoOnOwnConnection(divisorAddress(s), requestHeader)
				} else {
					err = bytesOnOwnConnection(divisorAddress(s), 128<<10+requestIndex*1031, requestHeader)
				}
				if err != nil {
					failures[clientIndex] = append(failures[clientIndex], err.Error())
				}
			}
		}()
	}
	clientsDone.Wait()
	if failed := slices.Concat(failures...); len(failed) > 0 {
		t.Fatalf("%d of %d answers did not arrive whole before the close; first ones: %v", len(failed), clientCount*requestsPerClient, failed[:min(5, len(failed))])
	}
}

func echoOnOwnConnection(address string, requestHeader http.Header) error {
	requestID := newRequestID()
	header := requestHeader.Clone()
	if header == nil {
		header = http.Header{}
	}
	header.Set(requestIDHeader, requestID)
	resp, body, err := answerOnOwnConnection(address, withIdentifyingQuery("/closing", requestID), header)
	if err != nil {
		return fmt.Errorf("%s: %w", requestID, err)
	}
	var echo EchoResp
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &echo) != nil {
		return fmt.Errorf("%s: status %d, body %.200s", requestID, resp.StatusCode, body)
	}
	if echoed := echo.Header(requestIDHeader); echoed != requestID || resp.Header.Get(requestIDHeader) != requestID {
		return fmt.Errorf("%s got the answer to request %q", requestID, echoed)
	}
	return nil
}

func bytesOnOwnConnection(address string, size int, requestHeader http.Header) error {
	seed := newBodySeed()
	resp, body, err := answerOnOwnConnection(address, fmt.Sprintf("/bytes?size=%d&seed=%d", size, seed), requestHeader)
	if err != nil {
		return fmt.Errorf("%d-byte answer of seed %d: %w", size, seed, err)
	}
	return verifyBytesAnswer(resp, body, size, seed)
}

// answerOnOwnConnection sends one request on a new connection and reads until divisor closes it: an answer cut
// short by the close fails its Content-Length, and a connection left open fails the read deadline.
func answerOnOwnConnection(address, target string, header http.Header) (*http.Response, []byte, error) {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return nil, nil, err
	}
	var request bytes.Buffer
	fmt.Fprintf(&request, "GET %s HTTP/1.1\r\nHost: divisor\r\n", target)
	for name, values := range header {
		for _, value := range values {
			fmt.Fprintf(&request, "%s: %s\r\n", name, value)
		}
	}
	request.WriteString("\r\n")
	if _, err := conn.Write(request.Bytes()); err != nil {
		return nil, nil, fmt.Errorf("writing the request: %w", err)
	}

	everythingUntilClose, err := io.ReadAll(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("divisor did not close the connection after its answer (%d bytes read): %w", len(everythingUntilClose), err)
	}
	answerBytes := bufio.NewReader(bytes.NewReader(everythingUntilClose))
	resp, err := http.ReadResponse(answerBytes, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the answer from the %d bytes before the close: %w", len(everythingUntilClose), err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("the connection closed before the whole answer arrived: %w", err)
	}
	if !resp.Close {
		return nil, nil, fmt.Errorf("the answer does not announce Connection: close")
	}
	if trailing := answerBytes.Buffered(); trailing > 0 {
		return nil, nil, fmt.Errorf("%d bytes followed the answer before the close", trailing)
	}
	return resp, body, nil
}
