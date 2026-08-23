package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
