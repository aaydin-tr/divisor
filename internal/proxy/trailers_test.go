package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestRequestTrailersReachBackendAsTrailers(t *testing.T) {
	var seenBody, seenTrailer, seenAsHeader string
	bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAsHeader = r.Header.Get("X-Checksum")
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		seenTrailer = r.Trailer.Get("X-Checksum")
	}))
	defer bServer.Close()

	b := config.Backend{Url: protocolRegex.ReplaceAllString(bServer.URL, "")}
	p := NewProxyClient(&b, nil, nil).(*ProxyClient)

	ctx := fasthttp.RequestCtx{Request: *fasthttp.AcquireRequest(), Response: *fasthttp.AcquireResponse()}
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetBodyString("hello")
	require.NoError(t, ctx.Request.Header.AddTrailer("X-Checksum"))
	ctx.Request.Header.Set("X-Checksum", "abc123")

	assert.NoError(t, p.ReverseProxyHandler(&ctx))
	assert.Equal(t, "hello", seenBody)
	assert.Equal(t, "abc123", seenTrailer, "a client trailer stays a trailer at the Backend")
	assert.Empty(t, seenAsHeader, "a trailer must not be promoted to a header")
}

// A Middleware calling Body() leaves the body buffered; fasthttp still writes
// a buffered body with registered trailers chunked, so they reach the Backend.
func TestMiddlewareReadingBodyKeepsTrailers(t *testing.T) {
	const chunkedWithTrailer = "POST /upload HTTP/1.1\r\nHost: divisor.example\r\n" +
		"Transfer-Encoding: chunked\r\nTrailer: X-Checksum\r\n\r\n" +
		"5\r\nhello\r\n0\r\nX-Checksum: abc123\r\n\r\n"
	for _, frontend := range frontends {
		t.Run(frontend.name, func(t *testing.T) {
			var seenByMiddleware string
			p, backendWire := newWireRecordingProxyClient(&mockMiddleware{onRequestFunc: func(ctx *middleware.Context) error {
				seenByMiddleware = string(ctx.Request.Body())
				return nil
			}})
			frontend.proxy(t, p, chunkedWithTrailer)

			wire := <-backendWire
			assert.Equal(t, "hello", seenByMiddleware)
			assert.Contains(t, wire, "\r\nTransfer-Encoding: chunked\r\n")
			assert.Equal(t, "hello", decodedBody(t, wire))
			assert.True(t, strings.HasSuffix(wire, "\r\n0\r\nX-Checksum: abc123\r\n\r\n"), "trailer after the last chunk: %q", wire)
		})
	}
}
