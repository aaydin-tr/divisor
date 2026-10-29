package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The tests here observe a Streamed response at the edges: when bytes reach a
// real client through each of divisor's real listener stacks, and what the
// Backend sees.

const waitLimit = 5 * time.Second

// frontend is a listener stack serving a ProxyClient, with a client that
// speaks its protocol.
type frontend struct {
	url    string
	client *http.Client
}

var listenerStacks = []struct {
	name          string
	startFrontend func(t *testing.T, p *ProxyClient, writeTimeout time.Duration) frontend
}{
	{"fasthttp", newFasthttpFrontend},
	{"net/http HTTP/2", newStreamingHTTP2Frontend},
	{"net/http HTTP/1.1", newStreamingHTTP2FrontendForHTTP1Client},
}

// noWriteTimeout is server.write_timeout's default: writes are unbounded.
const noWriteTimeout = 0

func onEveryListenerStack(t *testing.T, test func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend)) {
	t.Helper()
	onEveryListenerStackWithWriteTimeout(t, noWriteTimeout, test)
}

func onEveryListenerStackWithWriteTimeout(t *testing.T, writeTimeout time.Duration, test func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend)) {
	t.Helper()
	for _, stack := range listenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			test(t, func(t *testing.T, p *ProxyClient) frontend { return stack.startFrontend(t, p, writeTimeout) })
		})
	}
}

// newBackend ends every request it serves when the test ends: a net/http
// listener's cleanup waits for every stream still open through it.
func newBackend(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.BaseContext = func(net.Listener) context.Context { return t.Context() }
	server.Start()
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return protocolRegex.ReplaceAllString(server.URL, "")
}

func newStreamingProxyClient(backendAddr string, middlewares ...middleware.Middleware) *ProxyClient {
	return createTestProxyWithMiddlewares(config.Backend{
		Url:               backendAddr,
		ProxyTimeout:      time.Second,
		Streaming:         true,
		StreamIdleTimeout: time.Minute,
	}, nil, middlewares...)
}

// newFasthttpFrontend serves p like divisor's HTTP/1.1 listener.
func newFasthttpFrontend(t *testing.T, p *ProxyClient, writeTimeout time.Duration) frontend {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &fasthttp.Server{
		Handler: NewFasthttpHandler(&stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
			_ = p.ReverseProxyHandler(ctx)
		}}, writeTimeout),
		WriteTimeout: writeTimeout,
	}
	go server.Serve(listener) //nolint:errcheck
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.ShutdownWithContext(ctx)
	})
	return frontend{url: "http://" + listener.Addr().String(), client: oneShotClient}
}

// newStreamingHTTP2Frontend serves p like divisor's HTTP/2 listener.
func newStreamingHTTP2Frontend(t *testing.T, p *ProxyClient, writeTimeout time.Duration) frontend {
	t.Helper()
	server := newHTTP2FrontendWithWriteTimeout(t, p, writeTimeout)
	return frontend{url: server.URL, client: server.Client()}
}

// newStreamingHTTP2FrontendForHTTP1Client is the HTTP/2 listener serving a
// client that does not negotiate h2, which net/http answers over HTTP/1.1.
func newStreamingHTTP2FrontendForHTTP1Client(t *testing.T, p *ProxyClient, writeTimeout time.Duration) frontend {
	t.Helper()
	http2Frontend := newStreamingHTTP2Frontend(t, p, writeTimeout)
	trustedRoots := http2Frontend.client.Transport.(*http.Transport).TLSClientConfig.RootCAs //nolint:forcetypeassert
	return frontend{url: http2Frontend.url, client: &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: trustedRoots},
		DisableKeepAlives: true,
	}}}
}

func writeFrame(w http.ResponseWriter, frame string) {
	io.WriteString(w, frame) //nolint:errcheck
	w.(http.Flusher).Flush()
}

// waitFor reports whether released fired before the Backend's client (divisor)
// went away or the test ended.
func waitFor(r *http.Request, released <-chan struct{}) bool {
	select {
	case <-released:
		return true
	case <-r.Context().Done():
		return false
	}
}

type receiveResult[T any] struct {
	value T
	err   error
}

// within fails the test unless receive returns in time; a buffered response
// would otherwise block the test forever on a Backend that waits for it.
func within[T any](t *testing.T, limit time.Duration, receive func() (T, error)) (T, error) {
	t.Helper()
	done := make(chan receiveResult[T], 1)
	go func() {
		value, err := receive()
		done <- receiveResult[T]{value, err}
	}()
	select {
	case result := <-done:
		return result.value, result.err
	case <-time.After(limit):
		t.Fatalf("nothing arrived within %v", limit)
		var zero T
		return zero, nil
	}
}

// A kept-alive client connection would hold the frontend's Shutdown open.
var oneShotClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DisableCompression: true}}

func getWithin(t *testing.T, f frontend) *http.Response {
	t.Helper()
	res, err := within(t, waitLimit, func() (*http.Response, error) { return f.client.Get(f.url) }) //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func readFrameWithin(t *testing.T, body *bufio.Reader) string {
	t.Helper()
	frame, err := within(t, waitLimit, func() (string, error) { return body.ReadString('\n') })
	require.NoError(t, err)
	return frame
}

func TestStreamedResponseReachesClientBeforeBackendFinishes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header map[string]string
	}{
		{"length-less body", map[string]string{"Content-Type": "application/x-ndjson"}},
		{"event stream", map[string]string{"Content-Type": "text/event-stream; charset=utf-8"}},
		{"event stream with a declared length", map[string]string{
			"Content-Type":   "text/event-stream",
			"Content-Length": strconv.Itoa(len("frame-1\nframe-2\n")),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
				releaseSecondFrame := make(chan struct{})
				backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
					for name, value := range tc.header {
						w.Header().Set(name, value)
					}
					writeFrame(w, "frame-1\n")
					if waitFor(r, releaseSecondFrame) {
						writeFrame(w, "frame-2\n")
					}
				})

				res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
				body := bufio.NewReader(res.Body)
				assert.Equal(t, "frame-1\n", readFrameWithin(t, body))

				close(releaseSecondFrame)
				assert.Equal(t, "frame-2\n", readFrameWithin(t, body))
				_, err := body.ReadByte()
				assert.ErrorIs(t, err, io.EOF)
				assert.Equal(t, tc.header["Content-Type"], res.Header.Get("Content-Type"))
			})
		})
	}
}

// An EventSource opens on the headers; a Backend may send its first event
// much later.
func TestStreamedResponseHeadersArriveBeforeTheFirstFrame(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		releaseFirstFrame := make(chan struct{})
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			if waitFor(r, releaseFirstFrame) {
				writeFrame(w, "data: 0\n")
			}
		})

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
		assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

		close(releaseFirstFrame)
		assert.Equal(t, "data: 0\n", readFrameWithin(t, bufio.NewReader(res.Body)))
	})
}

func TestBufferedResponseArrivesOnlyWhenComplete(t *testing.T) {
	const wholeBody = "frame-1\nframe-2\n"
	for _, tc := range []struct {
		name      string
		streaming bool
		header    map[string]string
	}{
		{"declared length with streaming on", true, map[string]string{
			"Content-Type":   "application/json",
			"Content-Length": strconv.Itoa(len(wholeBody)),
		}},
		{"length-less body with streaming off", false, map[string]string{"Content-Type": "application/x-ndjson"}},
		{"event stream with streaming off", false, map[string]string{"Content-Type": "text/event-stream"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
				releaseSecondFrame := make(chan struct{})
				backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
					for name, value := range tc.header {
						w.Header().Set(name, value)
					}
					writeFrame(w, "frame-1\n")
					if waitFor(r, releaseSecondFrame) {
						writeFrame(w, "frame-2\n")
					}
				})
				p := newStreamingProxyClient(backendAddr)
				p.streaming = tc.streaming
				frontend := startFrontend(t, p)

				responses := make(chan *http.Response, 1)
				go func() {
					res, err := frontend.client.Get(frontend.url) //nolint:noctx
					if err == nil {
						responses <- res
					}
				}()
				select {
				case <-responses:
					t.Fatal("the response started before the Backend finished it")
				case <-time.After(200 * time.Millisecond):
				}

				close(releaseSecondFrame)
				var res *http.Response
				select {
				case res = <-responses:
				case <-time.After(waitLimit):
					t.Fatal("the response never arrived")
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				assert.Equal(t, wholeBody, string(body))
				assert.Equal(t, int64(len(wholeBody)), res.ContentLength)
			})
		})
	}
}

func TestStreamingBackendSilentBeforeFirstByteGets504(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			waitFor(r, nil)
		})
		p := newStreamingProxyClient(backendAddr)
		p.proxyTimeout = 100 * time.Millisecond

		res := getWithin(t, startFrontend(t, p))
		assert.Equal(t, http.StatusGatewayTimeout, res.StatusCode)
	})
}

func TestStreamOutlivesProxyTimeoutWhileDataFlows(t *testing.T) {
	const frameCount = 6
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for i := range frameCount {
				writeFrame(w, "data: "+strconv.Itoa(i)+"\n")
				time.Sleep(50 * time.Millisecond)
			}
		})
		p := newStreamingProxyClient(backendAddr)
		p.proxyTimeout = 100 * time.Millisecond
		p.streamIdleTimeout = time.Second

		res := getWithin(t, startFrontend(t, p))
		body := bufio.NewReader(res.Body)
		for i := range frameCount {
			assert.Equal(t, "data: "+strconv.Itoa(i)+"\n", readFrameWithin(t, body))
		}
		_, err := body.ReadByte()
		assert.ErrorIs(t, err, io.EOF)
	})
}

func TestSilentStreamEndsAtIdleTimeout(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendSawStreamClosed := make(chan struct{})
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeFrame(w, "data: 0\n")
			waitFor(r, nil)
			close(backendSawStreamClosed)
		})
		p := newStreamingProxyClient(backendAddr)
		p.proxyTimeout = time.Minute
		p.streamIdleTimeout = 150 * time.Millisecond

		res := getWithin(t, startFrontend(t, p))
		body := bufio.NewReader(res.Body)
		assert.Equal(t, "data: 0\n", readFrameWithin(t, body))

		silentSince := time.Now()
		_, err := within(t, waitLimit, func() (string, error) { return body.ReadString('\n') })
		// HTTP/1.1 cuts the connection (unexpected EOF); HTTP/2 resets the stream.
		require.Error(t, err)
		assert.NotErrorIs(t, err, io.EOF, "the client must see the stream cut, not a clean end")
		assert.GreaterOrEqual(t, time.Since(silentSince), 100*time.Millisecond)
		assert.Less(t, time.Since(silentSince), 2*time.Second)

		requireClosedWithin(t, backendSawStreamClosed, "the Backend's stream after the idle timeout")
	})
}

// newFloodingBackend sends a length-less body as fast as divisor takes it until
// divisor closes the stream, which it reports on the returned channel.
func newFloodingBackend(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	streamClosed := make(chan struct{})
	backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		block := bytes.Repeat([]byte("x"), 32<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(block); err != nil {
				break
			}
		}
		close(streamClosed)
	})
	return backendAddr, streamClosed
}

// A client that stays connected but stops reading blocks divisor's writes once
// the buffers between them fill; stream_idle_timeout bounds that wait too.
func TestStreamOfAClientThatStopsReadingEndsAtIdleTimeout(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		observed := observeDebugLog(t)
		backendAddr, backendStreamClosed := newFloodingBackend(t)
		p := newStreamingProxyClient(backendAddr)
		p.streamIdleTimeout = 300 * time.Millisecond

		getWithin(t, startFrontend(t, p))

		requireClosedWithin(t, backendStreamClosed, "the Backend stream of a client that stopped reading")
		requireStreamEndLogged(t, observed, backendAddr, "idle timeout")
	})
}

// newOpenEventStreamBackend sends one frame, then holds the stream open until
// divisor closes it, which it reports on the returned channel.
func newOpenEventStreamBackend(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	streamClosed := make(chan struct{})
	backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeFrame(w, "data: 0\n")
		waitFor(r, nil)
		close(streamClosed)
	})
	return backendAddr, streamClosed
}

// openStreamWithin opens f's stream with a request the returned cancel
// abandons, which is how a client disconnects.
func openStreamWithin(t *testing.T, f frontend) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, disconnect := context.WithCancel(context.Background())
	t.Cleanup(disconnect)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	require.NoError(t, err)
	res, err := within(t, waitLimit, func() (*http.Response, error) { return f.client.Do(req) })
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })
	return res, disconnect
}

func TestClientDisconnectAbortsBackendStream(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr, backendStreamClosed := newOpenEventStreamBackend(t)
		p := newStreamingProxyClient(backendAddr)

		res, disconnect := openStreamWithin(t, startFrontend(t, p))
		assert.Equal(t, "data: 0\n", readFrameWithin(t, bufio.NewReader(res.Body)))

		disconnect()
		requireClosedWithin(t, backendStreamClosed, "the Backend stream of a disconnected client")
		assert.Eventually(t, func() bool { return p.PendingRequests() == 0 }, waitLimit, 10*time.Millisecond)
	})
}

// Only a stream is aborted: a buffered request keeps ADR 0003's position, run
// to the Backend's answer (bounded by proxy_timeout) even once its client left.
func TestClientDisconnectLeavesBufferedRequestRunning(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		requestArrived := make(chan struct{})
		releaseResponse := make(chan struct{})
		backendSawCancellation := make(chan bool, 1)
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			close(requestArrived)
			select {
			case <-releaseResponse:
				backendSawCancellation <- false
				io.WriteString(w, "answer") //nolint:errcheck
			case <-r.Context().Done():
				backendSawCancellation <- true
			}
		})
		f := startFrontend(t, newStreamingProxyClient(backendAddr))

		ctx, disconnect := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
		require.NoError(t, err)
		go func() {
			if res, err := f.client.Do(req); err == nil {
				res.Body.Close()
			}
		}()
		select {
		case <-requestArrived:
		case <-time.After(waitLimit):
			t.Fatal("the request never reached the Backend")
		}

		disconnect()
		time.Sleep(200 * time.Millisecond) // far longer than a stream's abort takes
		close(releaseResponse)
		assert.False(t, <-backendSawCancellation, "the Backend request was cancelled")
	})
}

// observeDebugLog swaps the application logger for an observer core and
// restores it when the test ends.
func observeDebugLog(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, observed := observer.New(zapcore.DebugLevel)
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))
	return observed
}

func requireStreamEndLogged(t *testing.T, observed *observer.ObservedLogs, backendAddr, cause string) {
	t.Helper()
	streamEnds := func() []observer.LoggedEntry {
		return observed.FilterMessage("Streamed response ended").FilterField(zap.String("backend", backendAddr)).All()
	}
	require.Eventually(t, func() bool { return len(streamEnds()) > 0 }, waitLimit, 10*time.Millisecond, "no stream end logged")
	require.Len(t, streamEnds(), 1)
	assert.Equal(t, zapcore.DebugLevel, streamEnds()[0].Level)
	assert.Equal(t, cause, streamEnds()[0].ContextMap()["cause"])
}

func TestStreamEndIsLoggedWithItsCause(t *testing.T) {
	t.Run("backend closed", func(t *testing.T) {
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeDebugLog(t)
			backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeFrame(w, "data: 0\n")
			})

			res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
			_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
			require.NoError(t, err)
			requireStreamEndLogged(t, observed, backendAddr, "backend closed")
		})
	})

	t.Run("idle timeout", func(t *testing.T) {
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeDebugLog(t)
			backendAddr, _ := newOpenEventStreamBackend(t)
			p := newStreamingProxyClient(backendAddr)
			p.streamIdleTimeout = 150 * time.Millisecond

			res := getWithin(t, startFrontend(t, p))
			_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
			require.Error(t, err)
			requireStreamEndLogged(t, observed, backendAddr, "idle timeout")
		})
	})

	t.Run("client disconnected", func(t *testing.T) {
		onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
			observed := observeDebugLog(t)
			backendAddr, _ := newOpenEventStreamBackend(t)

			res, disconnect := openStreamWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
			assert.Equal(t, "data: 0\n", readFrameWithin(t, bufio.NewReader(res.Body)))
			disconnect()
			requireStreamEndLogged(t, observed, backendAddr, "client disconnected")
		})
	})
}

// server.write_timeout caps a stream's lifetime on every stack. net/http
// reports it by ending r.Context(), as it does for a client that went away.
func TestStreamCutByWriteTimeoutIsNotLoggedAsClientDisconnect(t *testing.T) {
	const writeTimeout = 300 * time.Millisecond
	onEveryListenerStackWithWriteTimeout(t, writeTimeout, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		observed := observeDebugLog(t)
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for {
				writeFrame(w, "data: tick\n")
				select {
				case <-r.Context().Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		})

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr)))
		_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		require.Error(t, err)
		requireStreamEndLogged(t, observed, backendAddr, "write timeout")
	})
}

// The fasthttp listener watches the client connection during a stream; the
// watch must hand it back intact for the next request.
func TestFasthttpClientConnectionServesNextRequestAfterStream(t *testing.T) {
	backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeFrame(w, "data: 0\n")
	})
	f := newFasthttpFrontend(t, newStreamingProxyClient(backendAddr), noWriteTimeout)
	keepAliveClient := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(keepAliveClient.CloseIdleConnections)

	for i := range 2 {
		var connectionReused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { connectionReused = info.Reused }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, f.url, nil)
		require.NoError(t, err)
		res, err := within(t, waitLimit, func() (*http.Response, error) { return keepAliveClient.Do(req) })
		require.NoError(t, err)
		body, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		res.Body.Close()
		require.NoError(t, err)
		assert.Equal(t, "data: 0\n", string(body))
		assert.Equal(t, i > 0, connectionReused, "request %d", i)
	}
}

// The watch consumes whatever a pipelining client sends mid-stream, so that
// connection must close rather than parse a request missing those bytes.
func TestFasthttpRequestPipelinedDuringStreamIsNeverForwardedTruncated(t *testing.T) {
	var forwardedRequests []string
	var forwardedRequestsMu sync.Mutex
	releaseStreamEnd := make(chan struct{})
	backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		forwardedRequestsMu.Lock()
		forwardedRequests = append(forwardedRequests, r.Method+" "+r.URL.Path)
		forwardedRequestsMu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		writeFrame(w, "data: 0\n")
		waitFor(r, releaseStreamEnd)
	})
	f := newFasthttpFrontend(t, newStreamingProxyClient(backendAddr), noWriteTimeout)

	conn, err := net.Dial("tcp", strings.TrimPrefix(f.url, "http://"))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = io.WriteString(conn, "GET /stream HTTP/1.1\r\nHost: divisor\r\n\r\n")
	require.NoError(t, err)
	res, err := within(t, waitLimit, func() (*http.Response, error) { return http.ReadResponse(bufio.NewReader(conn), nil) })
	require.NoError(t, err)
	body := bufio.NewReader(res.Body)
	assert.Equal(t, "data: 0\n", readFrameWithin(t, body))

	_, err = io.WriteString(conn, "GET /pipelined HTTP/1.1\r\nHost: divisor\r\n\r\n")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // lets the watch read the pipelined bytes
	close(releaseStreamEnd)

	_, err = within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(body) })
	assert.Error(t, err, "the connection closes instead of ending the stream cleanly")
	forwardedRequestsMu.Lock()
	defer forwardedRequestsMu.Unlock()
	assert.Equal(t, []string{"GET /stream"}, forwardedRequests)
}

func requireClosedWithin(t *testing.T, closed <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(waitLimit):
		t.Fatalf("%s stayed open", what)
	}
}

func TestOnResponseSeesStreamedStatusAndHeadersButNoBody(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr, _ := newOpenEventStreamBackend(t)
		bodyLenSeen := make(chan int, 1)
		mw := &mockMiddleware{onResponseFunc: func(ctx *middleware.Context, err error) error {
			bodyLenSeen <- len(ctx.Response.Body())
			ctx.Response.Header.Set("X-Mutated", "by-middleware")
			ctx.Response.SetBodyString("a rewrite that must not apply")
			return nil
		}}

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr, mw)))
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "by-middleware", res.Header.Get("X-Mutated"))
		assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
		assert.Equal(t, "data: 0\n", readFrameWithin(t, bufio.NewReader(res.Body)))
		assert.Equal(t, 0, <-bodyLenSeen)
	})
}

func TestOnResponseShortCircuitReplacesStream(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr, backendStreamClosed := newOpenEventStreamBackend(t)
		mw := &mockMiddleware{onResponseFunc: func(ctx *middleware.Context, err error) error {
			ctx.Response.SetStatusCode(http.StatusTeapot)
			ctx.Response.Header.SetContentType("text/plain")
			ctx.Response.SetBodyString("crafted")
			return middleware.ErrShortCircuit
		}}

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr, mw)))
		body, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		require.NoError(t, err)
		assert.Equal(t, http.StatusTeapot, res.StatusCode)
		assert.Equal(t, "crafted", string(body))
		requireClosedWithin(t, backendStreamClosed, "the replaced Backend stream")
	})
}

func TestOnResponseFailureOnStreamAnswers500(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr, backendStreamClosed := newOpenEventStreamBackend(t)
		mw := &mockMiddleware{onResponseFunc: func(ctx *middleware.Context, err error) error {
			return errors.New("broken middleware")
		}}

		res := getWithin(t, startFrontend(t, newStreamingProxyClient(backendAddr, mw)))
		body, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
		assert.JSONEq(t, `{"message":"broken middleware"}`, string(body))
		requireClosedWithin(t, backendStreamClosed, "the abandoned Backend stream")
	})
}

func TestStreamIsScoredByTimeToFirstByte(t *testing.T) {
	const timeToFirstByte = 100 * time.Millisecond
	const streamDuration = 600 * time.Millisecond
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(timeToFirstByte)
			w.Header().Set("Content-Type", "text/event-stream")
			writeFrame(w, "data: 0\n")
			time.Sleep(streamDuration)
			writeFrame(w, "data: 1\n")
		})
		p := newStreamingProxyClient(backendAddr)

		res := getWithin(t, startFrontend(t, p))
		_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(res.Body) })
		require.NoError(t, err)

		assert.GreaterOrEqual(t, p.RecentResponseTime(), float64(timeToFirstByte.Milliseconds()))
		assert.Less(t, p.RecentResponseTime(), float64(streamDuration.Milliseconds()))
		assert.InDelta(t, p.RecentResponseTime(), p.AvgResponseTime(), 0.001)
	})
}

func TestOpenStreamCountsAsPendingUntilItCloses(t *testing.T) {
	onEveryListenerStack(t, func(t *testing.T, startFrontend func(*testing.T, *ProxyClient) frontend) {
		releaseStreamEnd := make(chan struct{})
		backendAddr := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeFrame(w, "data: 0\n")
			waitFor(r, releaseStreamEnd)
		})
		p := newStreamingProxyClient(backendAddr)

		res := getWithin(t, startFrontend(t, p))
		body := bufio.NewReader(res.Body)
		assert.Equal(t, "data: 0\n", readFrameWithin(t, body))
		assert.Equal(t, 1, p.PendingRequests())

		close(releaseStreamEnd)
		_, err := within(t, waitLimit, func() ([]byte, error) { return io.ReadAll(body) })
		require.NoError(t, err)
		assert.Eventually(t, func() bool { return p.PendingRequests() == 0 }, waitLimit, 10*time.Millisecond)
	})
}
