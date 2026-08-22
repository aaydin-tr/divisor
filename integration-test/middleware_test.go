package integration

import (
	"net/http"
	"testing"
)

// Middleware sources are interpreted by yaegi inside the divisor container;
// they may import only the stdlib and github.com/aaydin-tr/divisor/middleware.

const injectorMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Injector struct {
	header string
	value  string
}

func (m *Injector) OnRequest(ctx *middleware.Context) error {
	ctx.Request.Header.Set(m.header, m.value)
	return nil
}

func (m *Injector) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	h, _ := config["header"].(string)
	v, _ := config["value"].(string)
	return &Injector{header: h, value: v}
}
`

const chainAppenderMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Appender struct {
	tag string
}

func (m *Appender) OnRequest(ctx *middleware.Context) error {
	prev := string(ctx.Request.Header.Peek("X-Chain"))
	ctx.Request.Header.Set("X-Chain", prev+m.tag)
	return nil
}

func (m *Appender) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	tag, _ := config["tag"].(string)
	return &Appender{tag: tag}
}
`

const responseHeaderMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Responder struct{}

func (m *Responder) OnRequest(ctx *middleware.Context) error {
	return nil
}

func (m *Responder) OnResponse(ctx *middleware.Context, err error) error {
	if err == nil {
		ctx.Response.Header.Set("X-Mw-Res", "1")
	}
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Responder{}
}
`

// abortMW short-circuits from OnRequest: the response it wrote goes out and
// no Backend is asked.
const abortMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Abort struct{}

func (m *Abort) OnRequest(ctx *middleware.Context) error {
	if string(ctx.Path()) == "/blocked" {
		ctx.SetStatusCode(403)
		ctx.SetBodyString("blocked by middleware")
		return middleware.ErrShortCircuit
	}
	return nil
}

func (m *Abort) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Abort{}
}
`

// failMW returns a plain error after writing a response: the written
// response must be discarded and divisor must answer 500.
const failMW = `
package mw

import (
	"errors"

	"github.com/aaydin-tr/divisor/middleware"
)

type Fail struct{}

func (m *Fail) OnRequest(ctx *middleware.Context) error {
	if string(ctx.Path()) == "/fail" {
		ctx.SetStatusCode(418)
		ctx.SetBodyString("this must not reach the client")
		return errors.New("rejected")
	}
	return nil
}

func (m *Fail) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Fail{}
}
`

// fallbackMW short-circuits from OnResponse on a Backend failure, replacing
// divisor's 502 with its own 200.
const fallbackMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Fallback struct{}

func (m *Fallback) OnRequest(ctx *middleware.Context) error {
	return nil
}

func (m *Fallback) OnResponse(ctx *middleware.Context, err error) error {
	if err != nil {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("X-Fallback", "1")
		ctx.SetBodyString("served from cache")
		return middleware.ErrShortCircuit
	}
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Fallback{}
}
`

const panicMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Panicker struct{}

func (m *Panicker) OnRequest(ctx *middleware.Context) error {
	if string(ctx.Path()) == "/panic" {
		panic("middleware boom")
	}
	return nil
}

func (m *Panicker) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Panicker{}
}
`

// markerMW stamps the request in OnRequest (response mutations made before
// proxy.Do would be wiped by the backend response); the Echo backend seeing
// the stamp proves the chain ran to the end.
const markerMW = `
package mw

import "github.com/aaydin-tr/divisor/middleware"

type Marker struct{}

func (m *Marker) OnRequest(ctx *middleware.Context) error {
	ctx.Request.Header.Set("X-Reached-End", "1")
	return nil
}

func (m *Marker) OnResponse(ctx *middleware.Context, err error) error {
	return nil
}

func New(config map[string]any) middleware.Middleware {
	return &Marker{}
}
`

func TestMiddlewareRequestChain(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "mwreq",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
		Middlewares: []MiddlewareSpec{
			{Name: "disabled-injector", Code: injectorMW, Disabled: true,
				Config: map[string]any{"header": "X-Disabled", "value": "should-not-appear"}},
			{Name: "injector", Code: injectorMW,
				Config: map[string]any{"header": "X-Injected", "value": "from-config"}},
			{Name: "chain-1", Code: chainAppenderMW, Config: map[string]any{"tag": "1"}},
			{Name: "chain-2", Code: chainAppenderMW, Config: map[string]any{"tag": "2"}},
		},
	})

	res := s.Get(t, "/mw")
	if got := res.Echo.Header("X-Injected"); got != "from-config" {
		t.Errorf("backend saw X-Injected=%q, want %q; middleware config map is not reaching the middleware", got, "from-config")
	}
	if got := res.Echo.Header("X-Chain"); got != "12" {
		t.Errorf("backend saw X-Chain=%q, want %q; middlewares must run in config order", got, "12")
	}
	if got := res.Echo.Header("X-Disabled"); got != "" {
		t.Errorf("disabled middleware ran and set X-Disabled=%q", got)
	}
}

func TestMiddlewareResponseAbortAndPanic(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "mwres",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
		Middlewares: []MiddlewareSpec{
			{Name: "responder", Code: responseHeaderMW},
			{Name: "abort", Code: abortMW},
			{Name: "fail", Code: failMW},
			{Name: "fallback", Code: fallbackMW},
			{Name: "panicker", Code: panicMW},
			{Name: "marker", Code: markerMW},
		},
	})

	t.Run("ResponseMutation", func(t *testing.T) {
		res := s.Get(t, "/ok")
		if res.Header.Get("X-Mw-Res") != "1" {
			t.Errorf("OnResponse mutation X-Mw-Res did not reach the client")
		}
		if res.Echo.Header("X-Reached-End") != "1" {
			t.Errorf("full chain did not run on a normal request")
		}
	})

	t.Run("AbortSkipsBackend", func(t *testing.T) {
		res, err := s.Request(http.MethodGet, "/blocked", nil, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("aborted request got status %d, want the middleware's 403", res.StatusCode)
		}
		if string(res.Body) != "blocked by middleware" {
			t.Errorf("aborted request body %q, want the middleware's body", res.Body)
		}
		if res.Header.Get("X-Backend-Id") != "" {
			t.Errorf("aborted request reached backend %s; an OnRequest short-circuit must skip the backend", res.Header.Get("X-Backend-Id"))
		}
		// Chain-stop-at-short-circuit has no direct observable here (the
		// backend is never contacted), but the positive case in
		// ResponseMutation proves the marker runs on unaborted requests.
	})

	t.Run("PlainErrorAnswers500", func(t *testing.T) {
		// ADR 0005: a non-short-circuit error means the middleware failed;
		// whatever it wrote is discarded and divisor answers 500.
		res, err := s.Request(http.MethodGet, "/fail", nil, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if res.StatusCode != http.StatusInternalServerError {
			t.Errorf("failing middleware got status %d, want 500", res.StatusCode)
		}
		if string(res.Body) != `{"message":"rejected"}` {
			t.Errorf("failing middleware body %q, want divisor's {\"message\":\"rejected\"}", res.Body)
		}
		if res.Header.Get("X-Backend-Id") != "" {
			t.Errorf("request reached backend %s although a middleware failed", res.Header.Get("X-Backend-Id"))
		}
	})

	t.Run("ShortCircuitReplacesBackendFailure", func(t *testing.T) {
		// fail_times=5 outlasts every retry, so the proxy attempt fails and
		// OnResponse sees the Backend error; its short-circuit must replace
		// divisor's 502.
		res, err := s.Request(http.MethodPost, "/retry?fail_key=mwfb&fail_times=5", nil, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("fallback got status %d, want the middleware's 200", res.StatusCode)
		}
		if string(res.Body) != "served from cache" {
			t.Errorf("fallback body %q, want the middleware's body", res.Body)
		}
		if res.Header.Get("X-Fallback") != "1" {
			t.Errorf("fallback header did not reach the client")
		}
	})

	t.Run("PanicIsRecovered", func(t *testing.T) {
		res, err := s.Request(http.MethodGet, "/panic", nil, nil)
		if err != nil {
			t.Fatalf("panicking middleware killed the connection: %v", err)
		}
		t.Logf("panicking middleware produced status %d", res.StatusCode)
		if res.Header.Get("X-Backend-Id") != "" {
			t.Errorf("request reached a backend although a middleware panicked")
		}

		// The panic must not take divisor down.
		after := s.Get(t, "/ok-after-panic")
		if after.Echo.Backend != "a" {
			t.Errorf("divisor stopped proxying after a middleware panic")
		}
	})
}

func TestMiddlewareFromFile(t *testing.T) {
	t.Parallel()
	s := startScenario(t, ScenarioSpec{
		Name:     "mwfile",
		Type:     "round-robin",
		Backends: []BackendSpec{{ID: "a"}},
		Middlewares: []MiddlewareSpec{
			{Name: "file-injector", Code: injectorMW, ViaFile: true,
				Config: map[string]any{"header": "X-From-File", "value": "yes"}},
		},
	})

	res := s.Get(t, "/mwfile")
	if got := res.Echo.Header("X-From-File"); got != "yes" {
		t.Errorf("file-based middleware did not run: X-From-File=%q", got)
	}
}
