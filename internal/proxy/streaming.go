package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

const eventStreamMediaType = "text/event-stream"

// forwardAllowingStream returns a Streamed response's body for the caller to
// forward after OnResponse; any other response is read whole into res.
func (h *ProxyClient) forwardAllowingStream(req *fasthttp.Request, res *fasthttp.Response, start time.Time) (*streamedBody, error) {
	// A separate Response: OnResponse must never see one still holding the
	// stream, which Response.Body() would read to the end.
	backendRes := fasthttp.AcquireResponse()
	backendRes.StreamBody = true
	err := h.doWithProxyTimeout(req, backendRes)
	if err != nil {
		fasthttp.ReleaseResponse(backendRes)
		h.recordFailure(time.Since(start))
		return nil, err
	}

	if !isStreamedResponse(backendRes) {
		err = moveWholeResponse(backendRes, res)
		fasthttp.ReleaseResponse(backendRes)
		h.recordOutcome(err, time.Since(start))
		return nil, err
	}

	// Scored at the first response byte: a stream's total duration says
	// nothing about how fast the Backend answers.
	h.recordResponseTime(time.Since(start))
	backendRes.CopyTo(res)
	h.openStreamCount.Add(1)
	return &streamedBody{
		backendResponse: backendRes,
		backendBody:     backendRes.BodyStream().(fasthttp.ReadCloserWithError), //nolint:forcetypeassert
		backendConn:     backendConnOf(backendRes),
		idleTimeout:     h.streamIdleTimeout,
		onEnd:           h.streamEnded,
	}, nil
}

// streamEnded runs once per Streamed response, on whichever goroutine ended it.
func (h *ProxyClient) streamEnded(end streamEnd) {
	h.openStreamCount.Add(-1)
	fields := []any{"backend", h.Addr, "cause", string(end.cause), "forwarded_bytes", end.forwardedBytes}
	if end.cause == streamEndBackendFailed {
		fields = append(fields, "error", end.err)
	}
	zap.S().Debugw("Streamed response ended", fields...)
}

// isStreamedResponse applies the streaming rule (CONTEXT.md, Streamed
// response): an event stream, or a body without a declared length.
func isStreamedResponse(res *fasthttp.Response) bool {
	if !res.IsBodyStream() {
		return false
	}
	return res.Header.ContentLength() < 0 || isEventStream(res.Header.ContentType())
}

func isEventStream(contentType []byte) bool {
	mediaType, _, err := mime.ParseMediaType(helper.B2S(contentType))
	return err == nil && mediaType == eventStreamMediaType
}

// moveWholeResponse reads the body under Do's read deadline, which fasthttp
// leaves on the connection, so proxy_timeout still bounds the whole response.
func moveWholeResponse(backendRes, res *fasthttp.Response) error {
	var body bytes.Buffer
	if backendRes.IsBodyStream() {
		if _, err := body.ReadFrom(backendRes.BodyStream()); err != nil {
			return asGatewayTimeout(err)
		}
	}
	backendRes.CopyTo(res)
	res.SetBodyRaw(body.Bytes())
	return nil
}

// asGatewayTimeout maps a read deadline expiring mid-body to ErrTimeout, which
// is what fasthttp reports when the deadline expires inside Do.
func asGatewayTimeout(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fasthttp.ErrTimeout
	}
	return err
}

// streamEndCause is why a Streamed response ended, as debug logs name it.
type streamEndCause string

const (
	streamEndBackendClosed        streamEndCause = "backend closed"
	streamEndBackendFailed        streamEndCause = "backend failed"
	streamEndIdleTimeout          streamEndCause = "idle timeout"
	streamEndClientDisconnected   streamEndCause = "client disconnected"
	streamEndWriteTimeout         streamEndCause = "write timeout"
	streamEndReplacedByMiddleware streamEndCause = "replaced by middleware"
)

type streamEnd struct {
	cause streamEndCause
	// err is what Read returns once the stream has ended.
	err            error
	forwardedBytes int64
}

// errClientGone ends the stream of a client that went away. fasthttp logs the
// error that ends a connection unless it reads like a dropped one, as this does.
var errClientGone = fmt.Errorf("client went away mid-stream: %w", io.ErrUnexpectedEOF)

// streamedBody is a Streamed response's body. Whoever ends it first names the
// cause: Read for the Backend, CloseWithError or a client watch for the client.
type streamedBody struct {
	backendResponse     *fasthttp.Response
	backendBody         fasthttp.ReadCloserWithError
	backendConn         *backendConn
	idleTimeout         time.Duration
	onEnd               func(streamEnd)
	stopWatchingClient  func() (clientSentData bool)
	clientWriteDeadline *clientWriteDeadline
	// Only the goroutine calling Read touches these two.
	bytesAwaitingWrite int
	backendReadErr     error
	forwardedBytes     atomic.Int64
	endOnce            sync.Once
	ended              atomic.Pointer[streamEnd]
}

// Read counts the bytes it returned once asked for more: both client stacks
// read again only after writing them all.
func (s *streamedBody) Read(p []byte) (int, error) {
	if end := s.ended.Load(); end != nil {
		return 0, end.err
	}
	s.forwardedBytes.Add(int64(s.bytesAwaitingWrite))
	s.bytesAwaitingWrite = 0
	if s.backendReadErr != nil {
		return 0, s.endWith(backendReadEndCause(s.backendReadErr), s.backendReadErr)
	}
	if s.backendConn != nil && s.idleTimeout > 0 {
		if err := s.backendConn.SetReadDeadline(time.Now().Add(s.idleTimeout)); err != nil {
			return 0, s.endWith(streamEndBackendFailed, err)
		}
	}
	n, err := s.backendBody.Read(p)
	if s.clientWriteDeadline != nil {
		s.clientWriteDeadline.arm()
	}
	if n == 0 && err != nil {
		return 0, s.endWith(backendReadEndCause(err), err)
	}
	// An error that came with bytes ends the stream on the next Read, once they are written.
	s.bytesAwaitingWrite, s.backendReadErr = n, err
	return n, nil
}

func backendReadEndCause(err error) streamEndCause {
	switch {
	case errors.Is(err, io.EOF):
		return streamEndBackendClosed
	case errors.Is(err, os.ErrDeadlineExceeded):
		return streamEndIdleTimeout
	default:
		return streamEndBackendFailed
	}
}

// endWith ends the stream unless it has ended already, and returns the error
// Read reports from then on.
func (s *streamedBody) endWith(cause streamEndCause, err error) error {
	s.endOnce.Do(func() {
		end := &streamEnd{cause: cause, err: err, forwardedBytes: s.forwardedBytes.Load()}
		s.ended.Store(end)
		if s.backendConn != nil {
			// A connection going back to the pool must not keep the idle deadline.
			s.backendConn.SetReadDeadline(time.Time{}) //nolint:errcheck
		}
		// A nil error returns a fully read connection to the pool; any other
		// interrupts a Read in flight and discards the connection.
		var backendCloseErr error
		if cause != streamEndBackendClosed {
			backendCloseErr = err
		}
		s.backendBody.CloseWithError(backendCloseErr) //nolint:errcheck
		fasthttp.ReleaseResponse(s.backendResponse)
		s.onEnd(*end)
	})
	return s.ended.Load().err
}

// CloseWithError implements fasthttp.ReadCloserWithError, called once the response is
// written or dropped: a stream the Backend has not ended stopped on the client side.
func (s *streamedBody) CloseWithError(err error) error {
	s.endWith(s.clientWriteEndCause(err), errClientGone) //nolint:errcheck
	if s.stopWatchingClient != nil && s.stopWatchingClient() {
		return errClientSentDataMidStream
	}
	return nil
}

func (s *streamedBody) clientWriteEndCause(writeErr error) streamEndCause {
	switch {
	case !errors.Is(writeErr, os.ErrDeadlineExceeded):
		return streamEndClientDisconnected
	case s.clientWriteDeadline != nil && !s.clientWriteDeadline.armedDeadlineIsWriteTimeout.Load():
		return streamEndIdleTimeout
	default:
		return streamEndWriteTimeout
	}
}

// boundClientWrites ends the stream of a client that stops reading: every write
// to it gets stream_idle_timeout, never past writeTimeoutExpiry (zero: none).
func (s *streamedBody) boundClientWrites(setWriteDeadline func(time.Time) error, writeTimeoutExpiry time.Time) {
	if s.idleTimeout <= 0 {
		return
	}
	s.clientWriteDeadline = &clientWriteDeadline{
		setWriteDeadline:   setWriteDeadline,
		idleTimeout:        s.idleTimeout,
		writeTimeoutExpiry: writeTimeoutExpiry,
		boundSince:         time.Now(),
	}
	s.clientWriteDeadline.arm()
}

// clientWriteDeadlineExpired reports whether a client write outlasted its deadline.
func (s *streamedBody) clientWriteDeadlineExpired() bool {
	return s.clientWriteDeadline != nil && s.clientWriteDeadline.expired()
}

type clientWriteDeadline struct {
	setWriteDeadline            func(time.Time) error
	idleTimeout                 time.Duration
	writeTimeoutExpiry          time.Time
	boundSince                  time.Time
	armedDeadlineSinceBound     atomic.Int64
	armedDeadlineIsWriteTimeout atomic.Bool
}

func (d *clientWriteDeadline) arm() {
	deadline := time.Now().Add(d.idleTimeout)
	atWriteTimeout := !d.writeTimeoutExpiry.IsZero() && d.writeTimeoutExpiry.Before(deadline)
	if atWriteTimeout {
		deadline = d.writeTimeoutExpiry
	}
	d.armedDeadlineSinceBound.Store(int64(deadline.Sub(d.boundSince)))
	d.armedDeadlineIsWriteTimeout.Store(atWriteTimeout)
	// A writer without deadlines (httptest.ResponseRecorder) leaves writes unbounded.
	d.setWriteDeadline(deadline) //nolint:errcheck
}

func (d *clientWriteDeadline) expired() bool {
	return time.Since(d.boundSince) >= time.Duration(d.armedDeadlineSinceBound.Load())
}

// writeTimeoutExpiresAt is when server.write_timeout cuts a response whose writing
// started at start; zero without a write_timeout.
func writeTimeoutExpiresAt(start time.Time, writeTimeout time.Duration) time.Time {
	if writeTimeout <= 0 {
		return time.Time{}
	}
	return start.Add(writeTimeout)
}

func streamedBodyOf(res *fasthttp.Response) (*streamedBody, bool) {
	stream, isStreamed := res.BodyStream().(*streamedBody)
	return stream, isStreamed
}

// forwardTo makes s the body of res. Whatever body res held is dropped: a
// Streamed response's body is the Backend's, never rewritten.
func (s *streamedBody) forwardTo(res *fasthttp.Response) {
	res.ImmediateHeaderFlush = true
	res.SetBodyStream(s, -1)
}

// logAccessAtEnd writes the request's Access log line when the stream ends, so
// the line covers the whole stream. Call it before the stream is first read.
func (s *streamedBody) logAccessAtEnd(requestAccessLog *logger.AccessLogEntry, start time.Time) {
	accessLog := *requestAccessLog
	streamEnded := s.onEnd
	s.onEnd = func(end streamEnd) {
		accessLog.Duration = time.Since(start)
		accessLog.BytesOut = int(end.forwardedBytes)
		streamEnded(end)
		logger.LogAccess(&accessLog)
	}
}

// fasthttp exposes no connection on a client Response but keeps its
// LocalAddr() (Response.ParseNetConn), so the address carries the connection.
type backendConn struct {
	net.Conn
	localAddr *backendConnAddr
}

type backendConnAddr struct {
	net.Addr
	conn *backendConn
}

func (c *backendConn) LocalAddr() net.Addr {
	return c.localAddr
}

func dialBackendConn(dial fasthttp.DialFunc) fasthttp.DialFunc {
	return func(addr string) (net.Conn, error) {
		conn, err := dial(addr)
		if err != nil {
			return nil, err
		}
		wrapped := &backendConn{Conn: conn}
		wrapped.localAddr = &backendConnAddr{Addr: conn.LocalAddr(), conn: wrapped}
		return wrapped, nil
	}
}

// backendConnOf is nil when the connection was not dialed by dialBackendConn;
// the stream then keeps fasthttp's proxy_timeout read deadline.
func backendConnOf(res *fasthttp.Response) *backendConn {
	if addr, ok := res.LocalAddr().(*backendConnAddr); ok {
		return addr.conn
	}
	return nil
}
