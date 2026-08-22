package proxy

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

type NetHttpAdapter struct {
	Balancer           types.IBalancer
	maxRequestBodySize int
}

func NewNetHttpAdapter(balancer types.IBalancer, maxRequestBodySize int) *NetHttpAdapter {
	return &NetHttpAdapter{Balancer: balancer, maxRequestBodySize: maxRequestBodySize}
}

func (a *NetHttpAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.maxRequestBodySize > 0 && r.ContentLength > int64(a.maxRequestBodySize) {
		writeBodyTooLarge(w)
		return
	}

	if a.maxRequestBodySize > 0 && r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody {
		// Mirror fasthttp's chunked-body handling: buffer a length-less body
		// up to the cap so an oversized payload never reaches a Backend.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(a.maxRequestBodySize)))
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
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ConvertNetHTTPRequestToFastHTTPRequest(r, &ctx)

	a.Balancer.Serve()(&ctx)

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

//nolint:gocyclo
func ConvertNetHTTPRequestToFastHTTPRequest(r *http.Request, ctx *fasthttp.RequestCtx) {
	ctx.Request.Header.SetMethod(r.Method)

	if r.Proto != "" {
		proto := r.Proto
		if r.ProtoAtLeast(2, 0) {
			proto = "HTTP/1.1"
		}
		ctx.Request.Header.SetProtocol(proto)
	}

	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	if host != "" {
		ctx.Request.Header.SetHost(host)
	}

	if r.RequestURI != "" {
		ctx.Request.SetRequestURI(r.RequestURI)
	} else if r.URL != nil {
		ctx.Request.SetRequestURI(r.URL.RequestURI())
	}

	for k, values := range r.Header {
		if strings.EqualFold(k, fasthttp.HeaderHost) {
			continue
		}
		for _, v := range values {
			ctx.Request.Header.Add(k, v)
		}
	}

	for k, values := range r.Trailer {
		if ctx.Request.Header.AddTrailer(k) != nil {
			continue
		}
		if len(values) > 0 {
			ctx.Request.Header.Set(k, strings.Join(values, ", "))
		}
	}

	if r.Close {
		ctx.Request.Header.Del(fasthttp.HeaderConnection)
		ctx.Request.SetConnectionClose()
	}

	if r.Body != nil && r.Body != http.NoBody {
		contentLength := int(r.ContentLength)
		if r.ContentLength <= 0 || r.ContentLength >= int64(math.MaxInt) {
			contentLength = -1
		}
		ctx.Request.SetBodyStream(r.Body, contentLength)
	}

	if r.RemoteAddr != "" {
		if remoteAddr := parseRemoteAddr(r.RemoteAddr); remoteAddr != nil {
			ctx.SetRemoteAddr(remoteAddr)
		}
	}

	scheme := ""
	if r.URL != nil {
		scheme = r.URL.Scheme
	}
	if scheme == "" && r.TLS != nil {
		scheme = "https"
	}
	if scheme != "" && scheme != "http" {
		ctx.Request.URI().SetScheme(scheme)
	}

	if r.URL != nil && r.URL.User != nil {
		uri := ctx.Request.URI()
		uri.SetUsername(r.URL.User.Username())
		if password, hasPassword := r.URL.User.Password(); hasPassword {
			uri.SetPassword(password)
		}
	}
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
