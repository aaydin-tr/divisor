package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// requestIDHeader carries a request's unique ID. The Echo backend echoes it in
// its JSON and, through the rh knob, as a response header, so an answer that
// belongs to another request shows on both.
const requestIDHeader = "X-Request-Id"

var lastRequestNumber atomic.Uint64

// newRequestID is unique across the suite run, so it can also name an Echo
// backend counter, an Access log path or a /bytes seed.
func newRequestID() string { return "req-" + strconv.FormatUint(lastRequestNumber.Add(1), 10) }

func newBodySeed() uint64 { return lastRequestNumber.Add(1) }

// identifyingQuery asks the Echo backend to answer with requestID and to count
// the request under it: fail_key with fail_times=0 never fails, so
// Echo.Counter(requestID) tells whether the request reached that Backend.
func identifyingQuery(requestID string) string {
	return "request_id=" + requestID +
		"&rh=" + url.QueryEscape(requestIDHeader+":"+requestID) +
		"&fail_key=" + requestID + "&fail_times=0"
}

func withIdentifyingQuery(pathAndQuery, requestID string) string {
	separator := "?"
	if strings.Contains(pathAndQuery, "?") {
		separator = "&"
	}
	return pathAndQuery + separator + identifyingQuery(requestID)
}

// requestShape gives the identified request at requestIndex of a client its
// path, query and extra headers.
type requestShape func(requestID string, requestIndex int) (pathAndQuery string, header http.Header)

func sameEchoRequest(pathAndQuery string) requestShape {
	return func(string, int) (string, http.Header) { return pathAndQuery, nil }
}

// requestOutcome is what one client saw for one identified request.
type requestOutcome struct {
	clientIndex  int
	requestIndex int
	requestID    string
	sentAt       time.Time
	latency      time.Duration
	// status is 0 when no complete answer arrived: err says why.
	status int
	// backend is the Echo backend that answered; empty for an answer divisor
	// or a Middleware wrote itself.
	backend         string
	answerRequestID string // the request ID on the answer's header
	// ownAnswerBody is the body of an answer no Echo backend sent.
	ownAnswerBody    string
	openedConnection bool
	// err is a transport failure, or an answer that belongs to another request.
	err error
}

func (o requestOutcome) String() string {
	switch {
	case o.noAnswerArrived():
		return fmt.Sprintf("%s failed after %s: %v", o.requestID, o.latency, o.err)
	case o.err != nil:
		return fmt.Sprintf("%s got status %d after %s: %v", o.requestID, o.status, o.latency, o.err)
	case o.backend == "":
		return fmt.Sprintf("%s got status %d after %s from no Backend: %.120s", o.requestID, o.status, o.latency, o.ownAnswerBody)
	default:
		return fmt.Sprintf("%s got status %d after %s from Backend %s", o.requestID, o.status, o.latency, o.backend)
	}
}

func (o requestOutcome) echoedOK() bool {
	return o.status == http.StatusOK && o.backend != "" && o.err == nil
}

func (o requestOutcome) noAnswerArrived() bool { return o.status == 0 }

// newSingleConnectionClient is one client on one keep-alive connection, as a
// browser tab or one SDK instance is.
func newSingleConnectionClient(s *Scenario) *http.Client {
	client := s.NewClient(30 * time.Second)
	client.Transport.(*http.Transport).MaxConnsPerHost = 1
	return client
}

func sendIdentifiedRequest(client *http.Client, s *Scenario, requestID string, clientIndex, requestIndex int, shape requestShape) requestOutcome {
	outcome := requestOutcome{clientIndex: clientIndex, requestIndex: requestIndex, requestID: requestID}
	pathAndQuery, header := shape(requestID, requestIndex)
	req, err := http.NewRequest(http.MethodGet, s.BaseURL+withIdentifyingQuery(pathAndQuery, requestID), http.NoBody)
	if err != nil {
		outcome.err = err
		return outcome
	}
	req.Header.Set(requestIDHeader, requestID)
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { outcome.openedConnection = !info.Reused },
	}))

	outcome.sentAt = time.Now()
	resp, err := client.Do(req)
	if err != nil {
		outcome.latency = time.Since(outcome.sentAt)
		outcome.err = err
		return outcome
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	outcome.latency = time.Since(outcome.sentAt)
	if err != nil {
		outcome.err = fmt.Errorf("reading the answer: %w", err)
		return outcome
	}
	outcome.status = resp.StatusCode
	outcome.answerRequestID = resp.Header.Get(requestIDHeader)
	outcome.backend = resp.Header.Get("X-Backend-Id")
	if outcome.backend == "" {
		outcome.ownAnswerBody = string(body)
		return outcome
	}
	var echo EchoResp
	if err := json.Unmarshal(body, &echo); err != nil || echo.Backend != outcome.backend {
		outcome.err = fmt.Errorf("the answer from Backend %s is not its echo reply: %.200s", outcome.backend, body)
		return outcome
	}
	if echoed := echo.Header(requestIDHeader); echoed != requestID || outcome.answerRequestID != requestID {
		outcome.err = fmt.Errorf("got the answer to another request: the Echo backend echoed %q, the answer's header says %q",
			echoed, outcome.answerRequestID)
	}
	return outcome
}

// runConcurrentClients starts clientCount clients at once, each sending
// requestsPerClient identified requests back to back on its own keep-alive
// connection, and returns every outcome once all are answered.
func runConcurrentClients(s *Scenario, clientCount, requestsPerClient int, shape requestShape) []requestOutcome {
	outcomesByClient := make([][]requestOutcome, clientCount)
	start := make(chan struct{})
	var clientsDone sync.WaitGroup
	for clientIndex := range clientCount {
		clientsDone.Add(1)
		go func() {
			defer clientsDone.Done()
			client := newSingleConnectionClient(s)
			defer client.CloseIdleConnections()
			<-start
			for requestIndex := range requestsPerClient {
				outcomesByClient[clientIndex] = append(outcomesByClient[clientIndex], sendIdentifiedRequest(client, s, newRequestID(), clientIndex, requestIndex, shape))
			}
		}()
	}
	close(start)
	clientsDone.Wait()
	return slices.Concat(outcomesByClient...)
}

// burstLoad is traffic in the background: every burst interval each of its
// clients sends one identified request, all at the same moment, so they reach
// divisor together as one batch of ready connections.
type burstLoad struct {
	stopOnce    sync.Once
	stopBursts  chan struct{}
	clientsDone sync.WaitGroup
	outcomesMu  sync.Mutex
	outcomes    []requestOutcome
}

func startBurstLoad(t *testing.T, s *Scenario, clientCount int, burstInterval time.Duration, shape requestShape) *burstLoad {
	t.Helper()
	load := &burstLoad{stopBursts: make(chan struct{})}
	burstSignals := make([]chan struct{}, clientCount)
	for clientIndex := range clientCount {
		burstSignals[clientIndex] = make(chan struct{}, 1)
		load.clientsDone.Add(1)
		go load.sendOnEveryBurst(s, clientIndex, burstSignals[clientIndex], shape)
	}
	go load.signalBursts(burstSignals, burstInterval)
	t.Cleanup(func() { load.stop() })
	return load
}

func (l *burstLoad) signalBursts(burstSignals []chan struct{}, burstInterval time.Duration) {
	ticker := time.NewTicker(burstInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopBursts:
			return
		case <-ticker.C:
			for _, burstSignal := range burstSignals {
				select {
				case burstSignal <- struct{}{}:
				default: // the client still waits for its previous answer
				}
			}
		}
	}
}

// sendOnEveryBurst stops at the client's first transport failure: its
// connection is gone, and every later request would fail the same way.
func (l *burstLoad) sendOnEveryBurst(s *Scenario, clientIndex int, burstSignal <-chan struct{}, shape requestShape) {
	defer l.clientsDone.Done()
	client := newSingleConnectionClient(s)
	defer client.CloseIdleConnections()
	for requestIndex := 0; ; requestIndex++ {
		select {
		case <-l.stopBursts:
			return
		case <-burstSignal:
		}
		select {
		case <-l.stopBursts: // a burst signal that came with the stop is not sent
			return
		default:
		}
		outcome := sendIdentifiedRequest(client, s, newRequestID(), clientIndex, requestIndex, shape)
		l.outcomesMu.Lock()
		l.outcomes = append(l.outcomes, outcome)
		l.outcomesMu.Unlock()
		if outcome.noAnswerArrived() {
			return
		}
	}
}

// outcomesSoFar returns the requests answered so far; the load keeps running.
func (l *burstLoad) outcomesSoFar() []requestOutcome {
	l.outcomesMu.Lock()
	defer l.outcomesMu.Unlock()
	return slices.Clone(l.outcomes)
}

// stopSending ends the bursts; requests already sent still get their answers.
func (l *burstLoad) stopSending() {
	l.stopOnce.Do(func() { close(l.stopBursts) })
}

// stop ends the bursts, waits for the answers to every request already sent
// and returns every outcome.
func (l *burstLoad) stop() []requestOutcome {
	l.stopSending()
	l.clientsDone.Wait()
	return l.outcomesSoFar()
}

func describeFirstOutcomes(outcomes []requestOutcome) string {
	described := make([]string, 0, 5)
	for _, o := range outcomes[:min(5, len(outcomes))] {
		described = append(described, o.String())
	}
	return strings.Join(described, "; ")
}

// requireEveryRequestEchoed fails the test unless every request got a 200
// from an Echo backend, answering that very request.
func requireEveryRequestEchoed(t *testing.T, outcomes []requestOutcome) {
	t.Helper()
	requireEveryRequestEchoedExcept(t, outcomes, func(requestOutcome) bool { return false })
}

// requireEveryRequestEchoedExcept is requireEveryRequestEchoed for a Scenario
// in which some requests are expected to fail.
func requireEveryRequestEchoedExcept(t *testing.T, outcomes []requestOutcome, expectedToFail func(requestOutcome) bool) {
	t.Helper()
	if len(outcomes) == 0 {
		t.Fatalf("no request was sent")
	}
	var failed []requestOutcome
	for _, o := range outcomes {
		if !o.echoedOK() && !expectedToFail(o) {
			failed = append(failed, o)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("%d of %d requests did not get their own 200 from an Echo backend; first ones: %s",
			len(failed), len(outcomes), describeFirstOutcomes(failed))
	}
}

// logLoadSummary names the load a Scenario ran, for reading a failure; status 0 counts transport failures.
func logLoadSummary(t *testing.T, outcomes []requestOutcome) {
	t.Helper()
	statusCounts := map[int]int{}
	for _, o := range outcomes {
		statusCounts[o.status]++
	}
	t.Logf("load: %d requests, statuses %v, served by %v", len(outcomes), statusCounts, countByBackend(outcomes))
}

func countByBackend(outcomes []requestOutcome) map[string]int {
	servedBy := map[string]int{}
	for _, o := range outcomes {
		if o.backend != "" {
			servedBy[o.backend]++
		}
	}
	return servedBy
}

func sentAfter(outcomes []requestOutcome, moment time.Time) []requestOutcome {
	var later []requestOutcome
	for _, o := range outcomes {
		if o.sentAt.After(moment) {
			later = append(later, o)
		}
	}
	return later
}

// requireEvenSpread allows each Backend 10% off its fair share: an exact split
// is today's round-robin implementation, not its contract.
func requireEvenSpread(t *testing.T, servedBy map[string]int, backendIDs ...string) {
	t.Helper()
	total := 0
	for _, count := range servedBy {
		total += count
	}
	fairShare := float64(total) / float64(len(backendIDs))
	for _, id := range backendIDs {
		if count := float64(servedBy[id]); count < fairShare*0.9 || count > fairShare*1.1 {
			t.Errorf("Backend %s served %v of %d requests, want about %.0f (served by: %v)", id, count, total, fairShare, servedBy)
		}
	}
}
