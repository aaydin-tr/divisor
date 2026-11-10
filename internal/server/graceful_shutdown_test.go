package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const requestIDHeader = "X-Request-Id"

// Two of these overflow fasthttp's 4096-byte write buffer, so pipelined answers leave a tail waiting in it.
var servedRequestAnswerBody = bytes.Repeat([]byte("served "), 400)

type servedRequestsBackend struct {
	*httptest.Server
	servedRequestIDs sync.Map
	servedCount      atomic.Int64
}

func startServedRequestsBackend(t *testing.T) *servedRequestsBackend {
	t.Helper()
	backend := &servedRequestsBackend{}
	backend.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		_, _ = io.Copy(io.Discard, r.Body)
		backend.servedRequestIDs.Store(requestID, struct{}{})
		backend.servedCount.Add(1)
		w.Header().Set(requestIDHeader, requestID)
		_, _ = w.Write(servedRequestAnswerBody)
	}))
	t.Cleanup(backend.Close)
	return backend
}

func (b *servedRequestsBackend) served(requestID string) bool {
	_, served := b.servedRequestIDs.Load(requestID)
	return served
}

type divisorInFrontOfBackend struct {
	server  Server
	address string
	backend *servedRequestsBackend
}

// startDivisorInFrontOfBackend runs the real proxy path: the stack's server, a round-robin Balancer over a Pool
// of one recording Backend, and that Backend's proxy client.
func startDivisorInFrontOfBackend(t *testing.T, stack listenerStack) divisorInFrontOfBackend {
	t.Helper()
	backend := startServedRequestsBackend(t)
	cfg := stack.newConfig(t)
	cfg.Backends = []config.Backend{{Url: backend.URL}}
	require.NoError(t, cfg.PrepareConfig())

	balancer := core.NewBalancer(cfg, nil, proxy.NewProxyClient)
	require.NotNil(t, balancer)
	t.Cleanup(func() { _ = balancer.Shutdown() })

	ln := localListener(t)
	srv, _, err := Start(cfg, balancer, ln)
	require.NoError(t, err)
	return divisorInFrontOfBackend{server: srv, address: ln.Addr().String(), backend: backend}
}

// sentRequest's err is nil once its own full answer arrived.
type sentRequest struct {
	requestID string
	err       error
}

type expectedAnswer struct {
	requestID  string
	protoMajor int
}

// check takes what client.Do or http.ReadResponse returned.
func (e expectedAnswer) check(res *http.Response, err error) error {
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	switch {
	case err != nil:
		return fmt.Errorf("reading the answer: %w", err)
	case res.ProtoMajor != e.protoMajor:
		return fmt.Errorf("an answer over HTTP/%d, want HTTP/%d", res.ProtoMajor, e.protoMajor)
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("status %d", res.StatusCode)
	case res.Header.Get(requestIDHeader) != e.requestID:
		return fmt.Errorf("the answer to request %q", res.Header.Get(requestIDHeader))
	case !bytes.Equal(body, servedRequestAnswerBody):
		return fmt.Errorf("an answer body of %d bytes, want %d", len(body), len(servedRequestAnswerBody))
	}
	return nil
}

type clientProtocol struct {
	name       string
	enable     func(*http.Protocols)
	protoMajor int
}

var (
	http1Client = clientProtocol{"an HTTP/1.1 client", enableHTTP1, 1}
	http2Client = clientProtocol{"an HTTP/2 client", enableHTTP2, 2}
)

// Go's client never retries a POST whose bytes it wrote, so a lost answer stays visible.
func sendKeepAlivePOSTsUntilOneFails(url string, protocol clientProtocol, newRequestID func() string) []sentRequest {
	client := newClientSpeaking(protocol.enable)
	client.Timeout = 30 * time.Second
	defer client.CloseIdleConnections()
	var sentRequests []sentRequest
	for {
		requestID := newRequestID()
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("payment=42"))
		if err == nil {
			req.Header.Set(requestIDHeader, requestID)
			err = expectedAnswer{requestID: requestID, protoMajor: protocol.protoMajor}.check(client.Do(req))
		}
		sentRequests = append(sentRequests, sentRequest{requestID: requestID, err: err})
		if err != nil {
			return sentRequests
		}
	}
}

const pipelinedRequestsPerWrite = 8

// Each write fits fasthttp's read buffer and follows the answers to the write before it: closing a connection
// with unread bytes in its socket resets it, which could destroy answers still on their way.
func sendPipelinedPOSTsUntilOneFails(stack listenerStack, address string, newRequestID func() string) []sentRequest {
	conn, err := stack.dial(address)
	if err != nil {
		return []sentRequest{{requestID: newRequestID(), err: err}}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	answers := bufio.NewReader(conn)
	var sentRequests []sentRequest
	for {
		var pipelinedRequests bytes.Buffer
		requestIDs := make([]string, pipelinedRequestsPerWrite)
		for i := range requestIDs {
			requestIDs[i] = newRequestID()
			fmt.Fprintf(&pipelinedRequests, "POST /work HTTP/1.1\r\nHost: %s\r\n%s: %s\r\nContent-Length: 10\r\n\r\npayment=42",
				address, requestIDHeader, requestIDs[i])
		}
		_, err := conn.Write(pipelinedRequests.Bytes())
		for _, requestID := range requestIDs {
			if err == nil {
				err = expectedAnswer{requestID: requestID, protoMajor: 1}.check(http.ReadResponse(answers, nil))
			}
			sentRequests = append(sentRequests, sentRequest{requestID: requestID, err: err})
		}
		if err != nil {
			return sentRequests
		}
	}
}

const (
	shutdownsUnderLoad           = 4
	loadClientCount              = 32
	servedRequestsBeforeShutdown = 200
)

// requireNoServedRequestGoesUnanswered shuts divisor down under load, a few times. Each load client sends on one
// connection until a request fails, which the shutdown makes happen: the connection closes and the listener is gone.
func requireNoServedRequestGoesUnanswered(t *testing.T, stack listenerStack, sendRequestsUntilOneFails func(address string, newRequestID func() string) []sentRequest) {
	t.Helper()
	var lastRequestNumber atomic.Int64
	newRequestID := func() string { return "request-" + strconv.FormatInt(lastRequestNumber.Add(1), 10) }

	for shutdownIndex := range shutdownsUnderLoad {
		divisor := startDivisorInFrontOfBackend(t, stack)
		sentRequestsByClient := make([][]sentRequest, loadClientCount)
		var clientsDone sync.WaitGroup
		for clientIndex := range loadClientCount {
			clientsDone.Add(1)
			go func() {
				defer clientsDone.Done()
				sentRequestsByClient[clientIndex] = sendRequestsUntilOneFails(divisor.address, newRequestID)
			}()
		}
		require.Eventually(t, func() bool { return divisor.backend.servedCount.Load() >= servedRequestsBeforeShutdown },
			10*time.Second, time.Millisecond, "the load never reached the Backend")

		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		require.NoError(t, divisor.server.Shutdown(shutdownContext))
		cancel()
		clientsDone.Wait()

		var answeredCount int
		var servedButUnanswered []string
		for _, sentRequests := range sentRequestsByClient {
			for _, request := range sentRequests {
				switch {
				case request.err == nil:
					answeredCount++
				case divisor.backend.served(request.requestID):
					servedButUnanswered = append(servedButUnanswered, fmt.Sprintf("%s: %v", request.requestID, request.err))
				}
			}
		}
		assert.NotZero(t, answeredCount, "shutdown %d: no request was answered", shutdownIndex)
		if len(servedButUnanswered) > 0 {
			t.Fatalf("shutdown %d: the Backend served %d requests whose clients got no answer; first ones: %v",
				shutdownIndex, len(servedButUnanswered), servedButUnanswered[:min(3, len(servedButUnanswered))])
		}
	}
}

// A request divisor forwards during a Graceful shutdown gets its answer: the Backend did the work, and for a POST
// the client would never learn it.
func TestGracefulShutdownUnderLoadAnswersEveryRequestTheBackendServed(t *testing.T) {
	for _, tc := range []struct {
		stack  listenerStack
		client clientProtocol
	}{
		{fasthttpStack, http1Client},
		{fasthttpOverTLSStack, http1Client},
		{netHttpStack, http1Client},
		{netHttpStack, http2Client},
	} {
		t.Run(tc.stack.name+" with "+tc.client.name, func(t *testing.T) {
			requireNoServedRequestGoesUnanswered(t, tc.stack, func(address string, newRequestID func() string) []sentRequest {
				return sendKeepAlivePOSTsUntilOneFails(tc.stack.url(address)+"/work", tc.client, newRequestID)
			})
		})
	}
}

// fasthttp holds an answer back while the next pipelined request is already read; a Graceful shutdown still
// sends it.
func TestGracefulShutdownAnswersEveryPipelinedRequestTheBackendServed(t *testing.T) {
	for _, stack := range fasthttpListenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			requireNoServedRequestGoesUnanswered(t, stack, func(address string, newRequestID func() string) []sentRequest {
				return sendPipelinedPOSTsUntilOneFails(stack, address, newRequestID)
			})
		})
	}
}
