package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/valyala/fasthttp"
)

type stubBalancer struct {
	handler func(ctx *fasthttp.RequestCtx)
}

func (s *stubBalancer) Serve() func(ctx *fasthttp.RequestCtx) { return s.handler }
func (s *stubBalancer) Stats() []types.ProxyStat              { return nil }
func (s *stubBalancer) Shutdown() error                       { return nil }

func TestNetHttpAdapterServeHTTP(t *testing.T) {
	balancer := &stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
		// The ctx must be initialized enough for middlewares that use the
		// full RequestCtx API, like on the fasthttp path.
		if ctx.Conn() == nil {
			t.Error("expected non-nil ctx.Conn()")
		}
		if ctx.ID() == 0 {
			t.Error("expected non-zero ctx.ID()")
		}
		if ctx.ConnTime().IsZero() {
			t.Error("expected non-zero ctx.ConnTime()")
		}
		select {
		case <-ctx.Done(): // must not panic and must not be closed
			t.Error("expected ctx.Done() to not be closed")
		default:
		}

		ctx.Response.SetStatusCode(fasthttp.StatusCreated)
		ctx.Response.Header.Add("Set-Cookie", "a=1")
		ctx.Response.Header.Add("Set-Cookie", "b=2")
		ctx.Response.Header.Set("X-Backend", "ok")
		ctx.Response.Header.Set("Connection", "close") // hop-by-hop, must not be forwarded
		ctx.Response.SetBodyString("hello")
	}}

	adapter := NewNetHttpAdapter(balancer, 0)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)
	rec := httptest.NewRecorder()

	adapter.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, res.StatusCode)
	}

	cookies := res.Header.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Errorf("expected 2 Set-Cookie headers, got %d: %v", len(cookies), cookies)
	}

	if res.Header.Get("X-Backend") != "ok" {
		t.Errorf("expected X-Backend ok, got %q", res.Header.Get("X-Backend"))
	}

	if res.Header.Get("Server") != "divisor" {
		t.Errorf("expected Server divisor, got %q", res.Header.Get("Server"))
	}

	if _, ok := res.Header["Connection"]; ok {
		t.Errorf("expected hop-by-hop Connection header to be dropped, got %q", res.Header.Get("Connection"))
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unexpected error reading body: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("expected body hello, got %q", body)
	}
}

func TestNetHttpAdapterMaxRequestBodySize(t *testing.T) {
	const limit = 64

	newAdapter := func(reached *bool, streamed *int) *NetHttpAdapter {
		return NewNetHttpAdapter(&stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
			*reached = true
			n, _ := io.Copy(io.Discard, ctx.Request.BodyStream())
			*streamed = int(n)
			ctx.Response.SetStatusCode(fasthttp.StatusOK)
		}}, limit)
	}

	t.Run("DeclaredLengthOverLimit", func(t *testing.T) {
		var reached bool
		var streamed int
		req := httptest.NewRequest(http.MethodPost, "http://example.com/x", bytes.NewReader(make([]byte, limit+1)))
		rec := httptest.NewRecorder()

		newAdapter(&reached, &streamed).ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", rec.Code)
		}
		if reached {
			t.Error("oversized body reached the balancer")
		}
	})

	t.Run("LengthlessBodyOverLimit", func(t *testing.T) {
		var reached bool
		var streamed int
		req := httptest.NewRequest(http.MethodPost, "http://example.com/x", io.NopCloser(bytes.NewReader(make([]byte, limit*10))))
		req.ContentLength = -1
		rec := httptest.NewRecorder()

		newAdapter(&reached, &streamed).ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", rec.Code)
		}
		if reached {
			t.Error("oversized length-less body reached the balancer")
		}
	})

	t.Run("LengthlessBodyUnderLimit", func(t *testing.T) {
		var reached bool
		var streamed int
		req := httptest.NewRequest(http.MethodPost, "http://example.com/x", io.NopCloser(bytes.NewReader(make([]byte, limit))))
		req.ContentLength = -1
		rec := httptest.NewRecorder()

		newAdapter(&reached, &streamed).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if streamed != limit {
			t.Errorf("balancer streamed %d bytes, want %d", streamed, limit)
		}
	})
}

// A middleware rewriting a response body via SetBodyString leaves the parsed
// backend Content-Length untouched; forwarding it truncated longer rewrites
// and broke shorter ones. The adapter must let net/http derive the length
// from the bytes actually written, so this test needs a real server — the
// httptest recorder does not enforce Content-Length.
func TestNetHttpAdapterIgnoresStaleContentLength(t *testing.T) {
	cases := []struct {
		name    string
		staleCL int
		body    string
	}{
		{"rewrite longer than stale length", 5, "goodbye world"},
		{"rewrite shorter than stale length", 500, "hi"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			balancer := &stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
				ctx.Response.SetStatusCode(fasthttp.StatusOK)
				ctx.Response.Header.SetContentLength(tc.staleCL)
				ctx.Response.SetBodyString(tc.body)
			}}

			server := httptest.NewServer(NewNetHttpAdapter(balancer, 0))
			defer server.Close()

			res, err := http.Get(server.URL)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer res.Body.Close()

			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}
			if string(got) != tc.body {
				t.Errorf("client received %q, want %q", got, tc.body)
			}
		})
	}
}

// trailerBody mimics net/http: the announced trailer's value appears in
// r.Trailer only once the body has been read to EOF.
type trailerBody struct {
	io.Reader
	trailer http.Header
	value   string
}

func (b *trailerBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.trailer.Set("X-Checksum", b.value)
	}
	return n, err
}

func (b *trailerBody) Close() error { return nil }

func TestNetHttpAdapterForwardsAnnouncedTrailers(t *testing.T) {
	var seenBody string
	var seenTrailer string
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		seenTrailer = r.Trailer.Get("X-Checksum")
	}))
	defer backendServer.Close()

	backend := config.Backend{Url: protocolRegex.ReplaceAllString(backendServer.URL, "")}
	proxyClient := NewProxyClient(&backend, nil, nil).(*ProxyClient)
	adapter := NewNetHttpAdapter(&stubBalancer{handler: func(ctx *fasthttp.RequestCtx) {
		_ = proxyClient.ReverseProxyHandler(ctx)
	}}, 1<<20)

	for _, declaredLength := range []int64{-1, 5} {
		t.Run(fmt.Sprintf("ContentLength=%d", declaredLength), func(t *testing.T) {
			seenBody, seenTrailer = "", ""
			trailer := http.Header{"X-Checksum": nil}
			req := httptest.NewRequest(http.MethodPost, "http://example.com/upload", nil)
			req.Body = &trailerBody{Reader: strings.NewReader("hello"), trailer: trailer, value: "abc123"}
			req.ContentLength = declaredLength
			req.Trailer = trailer
			rec := httptest.NewRecorder()

			adapter.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if seenBody != "hello" {
				t.Errorf("backend body = %q, want %q", seenBody, "hello")
			}
			if seenTrailer != "abc123" {
				t.Errorf("backend trailer X-Checksum = %q, want %q", seenTrailer, "abc123")
			}
		})
	}
}
