package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"

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
	ctx.Response.BodyWriteTo(w) //nolint:errcheck
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
