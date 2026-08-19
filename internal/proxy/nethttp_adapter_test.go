package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaydin-tr/divisor/core/types"
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
