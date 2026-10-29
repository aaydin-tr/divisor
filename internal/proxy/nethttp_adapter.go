package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

type NetHttpAdapter struct {
	handler            func(ctx *fasthttp.RequestCtx)
	maxRequestBodySize int
}

func NewNetHttpAdapter(balancer types.IBalancer, maxRequestBodySize int) *NetHttpAdapter {
	return &NetHttpAdapter{handler: balancer.Serve(), maxRequestBodySize: maxRequestBodySize}
}

func (a *NetHttpAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	servedSince := time.Now()
	if a.maxRequestBodySize > 0 && r.ContentLength > int64(a.maxRequestBodySize) {
		writeBodyTooLarge(w)
		return
	}
	dropEmptyBody(r)

	if a.mustBufferBody(r) {
		// Length-less bodies mirror fasthttp's chunked-body handling: buffered
		// up to the cap so an oversized payload never reaches a Backend.
		reader := r.Body
		if a.maxRequestBodySize > 0 {
			reader = http.MaxBytesReader(w, r.Body, int64(a.maxRequestBodySize))
		}
		body, err := io.ReadAll(reader)
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesErr):
			writeBodyTooLarge(w)
			return
		case err != nil:
			w.Header().Set("Server", "divisor")
			http.Error(w, "error reading request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, parseRemoteAddr(r.RemoteAddr), nil)
	fasthttpadaptor.ConvertNetHTTPRequestToFastHTTPRequest(r, &ctx.Request)

	a.handler(&ctx)

	ctx.Response.Header.All()(func(k []byte, v []byte) bool {
		// Content-Length is skipped because fasthttp's Response.SetBody (what
		// an OnResponse middleware uses to rewrite a body) does not update the
		// stored value; net/http derives framing from the bytes written.
		if !isHopHeader(k) && !bytes.EqualFold(k, contentLengthHeader) {
			w.Header().Add(helper.B2S(k), helper.B2S(v))
		}
		return true
	})
	w.Header().Set("Server", "divisor")

	w.WriteHeader(ctx.Response.StatusCode())
	if stream, isStreamed := streamedBodyOf(&ctx.Response); isStreamed {
		forwardStreamedBody(w, r, servedSince, stream)
		return
	}
	ctx.Response.BodyWriteTo(w) //nolint:errcheck
}

// forwardStreamedBody sends the headers at once, then each Read of the
// Backend stream as it arrives, until either side ends the stream.
func forwardStreamedBody(w http.ResponseWriter, r *http.Request, servedSince time.Time, stream *streamedBody) {
	controller := http.NewResponseController(w)
	writeTimeout := serverWriteTimeout(r)
	stream.boundClientWrites(controller.SetWriteDeadline, writeTimeoutExpiresAt(servedSince, writeTimeout))
	endFromClientSide := func() {
		stream.endWith(clientContextEndCause(stream, servedSince, writeTimeout), errClientGone) //nolint:errcheck
	}
	stopWatchingClient := context.AfterFunc(r.Context(), endFromClientSide)
	defer stopWatchingClient()

	err := controller.Flush()
	if err == nil {
		_, err = io.Copy(flushingWriter{writer: w, controller: controller}, stream)
	}
	if err != nil {
		// A no-op when the Backend side ended the stream first.
		endFromClientSide()
		// net/http's way to abort a response, as ReverseProxy does: the client
		// sees the stream reset or the connection cut, never a clean end.
		panic(http.ErrAbortHandler)
	}
}

func serverWriteTimeout(r *http.Request) time.Duration {
	if server, ok := r.Context().Value(http.ServerContextKey).(*http.Server); ok {
		return server.WriteTimeout
	}
	return 0
}

// net/http arms server.write_timeout before ServeHTTP runs.
const writeTimeoutHeadStart = 100 * time.Millisecond

// net/http ends r.Context() for a client that went away and for a missed write
// deadline alike, without saying which: only the clock tells them apart.
func clientContextEndCause(stream *streamedBody, servedSince time.Time, writeTimeout time.Duration) streamEndCause {
	switch {
	case writeTimeout > 0 && time.Since(servedSince) >= writeTimeout-writeTimeoutHeadStart:
		return streamEndWriteTimeout
	case stream.clientWriteDeadlineExpired():
		return streamEndIdleTimeout
	default:
		return streamEndClientDisconnected
	}
}

// flushingWriter hands every write to the client at once; net/http would hold
// it until its buffer fills or the handler returns.
type flushingWriter struct {
	writer     io.Writer
	controller *http.ResponseController
}

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.writer.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.controller.Flush()
}

// net/http's HTTP/2 server hands every request a non-nil Body, which would be
// forwarded as an empty chunked one. ContentLength 0 guarantees it is empty;
// an unknown length is probed for the methods net/http's own client probes.
func dropEmptyBody(r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	if r.ContentLength == 0 {
		r.Body = http.NoBody
		return
	}
	if r.ContentLength > 0 || !methodUsuallyLacksBody(r.Method) {
		return
	}
	body := bufio.NewReader(r.Body)
	if _, err := body.Peek(1); errors.Is(err, io.EOF) {
		r.Body = http.NoBody
		return
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{body, r.Body}
}

// Mirrors net/http's requestMethodUsuallyLacksBody.
func methodUsuallyLacksBody(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions, "PROPFIND", "SEARCH":
		return true
	}
	return false
}

func (a *NetHttpAdapter) mustBufferBody(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	return a.maxRequestBodySize > 0 && r.ContentLength < 0
}

func writeBodyTooLarge(w http.ResponseWriter) {
	w.Header().Set("Server", "divisor")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	w.Write(helper.S2B(bodyTooLargeMessage)) //nolint:errcheck
}

// isHopHeader reports whether key is a hop-by-hop header. Hop-by-hop headers
// are connection-specific and must not be forwarded to the client; HTTP/2
// even forbids them.
func isHopHeader(key []byte) bool {
	for _, hop := range hopHeaders {
		if bytes.EqualFold(key, hop) {
			return true
		}
	}
	return false
}

// parseRemoteAddr parses an http.Request.RemoteAddr into a net.Addr. It only
// parses the string and never resolves host names, so it cannot block.
// net/http sets RemoteAddr to an "IP:port" pair, but a bare IP without a port
// is accepted too. It returns nil for any other value.
func parseRemoteAddr(addr string) net.Addr {
	if addrPort, err := netip.ParseAddrPort(addr); err == nil {
		return net.TCPAddrFromAddrPort(addrPort)
	}
	if ip, err := netip.ParseAddr(addr); err == nil {
		return net.TCPAddrFromAddrPort(netip.AddrPortFrom(ip, 0))
	}
	return nil
}
