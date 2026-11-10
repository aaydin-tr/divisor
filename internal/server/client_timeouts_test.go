package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/mocks"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each slow client below outlasts these bounds several times over.
const (
	shortClientTimeout   = 300 * time.Millisecond
	slowClientByteGap    = 50 * time.Millisecond
	disconnectedPromptly = 2 * time.Second
)

const slowUploadBody = "twenty bytes of body"

func startServingWithClientTimeouts(t *testing.T, stack listenerStack, setTimeouts func(*config.Server)) string {
	t.Helper()
	cfg := stack.newConfig(t)
	cfg.Backends = []config.Backend{{Url: "localhost:8080"}}
	setTimeouts(&cfg.Server)
	require.NoError(t, cfg.PrepareConfig())

	ln := localListener(t)
	srv, _, err := Start(cfg, &mocks.MockBalancer{}, ln)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return ln.Addr().String()
}

func dialClient(t *testing.T, stack listenerStack, address string) net.Conn {
	t.Helper()
	conn, err := stack.dial(address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	return conn
}

// writeSlowly sends data a byte at a time, the way a slowloris client does.
func writeSlowly(conn net.Conn, data string) error {
	for i := range len(data) {
		if _, err := conn.Write([]byte{data[i]}); err != nil {
			return err
		}
		time.Sleep(slowClientByteGap)
	}
	return nil
}

// requireClosedByDivisorWithin reads until the connection ends; the client's own read deadline expiring means
// divisor kept the connection open.
func requireClosedByDivisorWithin(t *testing.T, conn net.Conn, bound time.Duration) {
	t.Helper()
	start := time.Now()
	_, err := io.Copy(io.Discard, conn)
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(), "divisor kept the connection open")
	assert.Less(t, time.Since(start), bound)
}

func sendRequest(t *testing.T, conn net.Conn, reader *bufio.Reader, request string) *http.Response {
	t.Helper()
	_, err := conn.Write([]byte(request))
	require.NoError(t, err)
	res, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res
}

func TestAClientSendingItsHeadersSlowlyIsDisconnectedAtReadHeaderTimeout(t *testing.T) {
	for _, stack := range everyListenerStack {
		t.Run(stack.name, func(t *testing.T) {
			address := startServingWithClientTimeouts(t, stack, func(s *config.Server) { s.ReadHeaderTimeout = shortClientTimeout })
			conn := dialClient(t, stack, address)

			slowHeaders := "GET / HTTP/1.1\r\nHost: divisor\r\nX-Slow: " + strings.Repeat("a", 60) + "\r\n\r\n"
			go func() { _ = writeSlowly(conn, slowHeaders) }()

			requireClosedByDivisorWithin(t, conn, disconnectedPromptly)
		})
	}
}

// Before read_header_timeout existed, read_timeout bounded the headers too; a shorter one still does.
func TestAReadTimeoutShorterThanReadHeaderTimeoutBoundsTheHeaders(t *testing.T) {
	for _, stack := range everyListenerStack {
		t.Run(stack.name, func(t *testing.T) {
			address := startServingWithClientTimeouts(t, stack, func(s *config.Server) { s.ReadTimeout = shortClientTimeout })
			conn := dialClient(t, stack, address)

			slowHeaders := "GET / HTTP/1.1\r\nHost: divisor\r\nX-Slow: " + strings.Repeat("a", 60) + "\r\n\r\n"
			go func() { _ = writeSlowly(conn, slowHeaders) }()

			requireClosedByDivisorWithin(t, conn, disconnectedPromptly)
		})
	}
}

func TestAnIdleKeepAliveConnectionIsClosedAtIdleTimeout(t *testing.T) {
	for _, stack := range everyListenerStack {
		t.Run(stack.name, func(t *testing.T) {
			address := startServingWithClientTimeouts(t, stack, func(s *config.Server) { s.IdleTimeout = shortClientTimeout })
			conn := dialClient(t, stack, address)

			res := sendRequest(t, conn, bufio.NewReader(conn), "GET / HTTP/1.1\r\nHost: divisor\r\n\r\n")
			require.Equal(t, http.StatusOK, res.StatusCode)

			requireClosedByDivisorWithin(t, conn, disconnectedPromptly)
		})
	}
}

// read_header_timeout bounds the headers only: with read_timeout unset, an upload slower than it still arrives,
// on a new connection and on a keep-alive one.
func TestASlowUploadOutlastingReadHeaderTimeoutIsAnswered(t *testing.T) {
	for _, stack := range everyListenerStack {
		t.Run(stack.name, func(t *testing.T) {
			address := startServingWithClientTimeouts(t, stack, func(s *config.Server) { s.ReadHeaderTimeout = shortClientTimeout })
			conn := dialClient(t, stack, address)
			reader := bufio.NewReader(conn)

			for range 2 {
				_, err := conn.Write([]byte("POST / HTTP/1.1\r\nHost: divisor\r\nContent-Length: 20\r\n\r\n"))
				require.NoError(t, err)
				require.NoError(t, writeSlowly(conn, slowUploadBody))
				res, err := http.ReadResponse(reader, nil)
				require.NoError(t, err)
				_ = res.Body.Close()
				assert.Equal(t, http.StatusOK, res.StatusCode)
			}
		})
	}
}

// fasthttp only: net/http hands the handler a body still being read, so the no-op balancer answers before
// read_timeout can cut it.
func TestAReadTimeoutStillBoundsASlowUpload(t *testing.T) {
	for _, stack := range fasthttpListenerStacks {
		t.Run(stack.name, func(t *testing.T) {
			address := startServingWithClientTimeouts(t, stack, func(s *config.Server) { s.ReadTimeout = shortClientTimeout })
			conn := dialClient(t, stack, address)

			go func() {
				_, _ = conn.Write([]byte("POST / HTTP/1.1\r\nHost: divisor\r\nContent-Length: 20\r\n\r\n"))
				_ = writeSlowly(conn, slowUploadBody)
			}()

			start := time.Now()
			res, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err == nil {
				_ = res.Body.Close()
				assert.NotEqual(t, http.StatusOK, res.StatusCode, "an upload slower than read_timeout was answered")
			}
			assert.Less(t, time.Since(start), disconnectedPromptly)
		})
	}
}
