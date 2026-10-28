package http

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type statusServer struct {
	status atomic.Int32
}

func (s *statusServer) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	status := int(s.status.Load())
	if status >= 300 && status < 400 {
		res.Header().Set("Location", "/elsewhere")
	}
	res.WriteHeader(status)
}

func TestNewHttpClient(t *testing.T) {
	client := NewHttpClient(time.Minute, time.Second)
	assert.IsType(t, client, &HttpClient{})
	assert.IsType(t, client.client, &fasthttp.Client{})
}

func TestProbeDialerUsesConfiguredDNSCacheDuration(t *testing.T) {
	dialer := newProbeDialer(2 * time.Second)
	assert.Equal(t, 2*time.Second, dialer.DNSCacheDuration)
}

func TestIsHostAlive(t *testing.T) {
	client := NewHttpClient(time.Minute, 5*time.Second)
	handler := &statusServer{}
	server := httptest.NewServer(handler)
	defer server.Close()

	const anyStatus = types.AnySuccessOrRedirect
	probeRuleCases := []struct {
		answeredStatus int
		expectedStatus types.ExpectedProbeStatus
		wantAlive      bool
	}{
		{200, anyStatus, true}, {201, anyStatus, true}, {204, anyStatus, true}, {299, anyStatus, true},
		{301, anyStatus, true}, {302, anyStatus, true}, {304, anyStatus, true}, {399, anyStatus, true},
		{400, anyStatus, false}, {401, anyStatus, false}, {404, anyStatus, false},
		{500, anyStatus, false}, {503, anyStatus, false},

		{204, 204, true}, {200, 204, false}, {201, 204, false},
		{302, 302, true}, {200, 302, false}, {301, 302, false},
		{200, 200, true}, {204, 200, false}, {304, 200, false},
		{503, 503, true}, {200, 503, false}, {500, 503, false},
	}
	for _, probeCase := range probeRuleCases {
		t.Run(fmt.Sprintf("answered %d, expected %d", probeCase.answeredStatus, probeCase.expectedStatus), func(t *testing.T) {
			handler.status.Store(int32(probeCase.answeredStatus))
			assert.Equal(t, probeCase.wantAlive, client.IsHostAlive(server.URL, probeCase.expectedStatus))
		})
	}

	t.Run("redirect is not followed", func(t *testing.T) {
		var hits int32
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}))
		defer redirecting.Close()
		assert.True(t, client.IsHostAlive(redirecting.URL, types.AnySuccessOrRedirect))
		assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
	})

	t.Run("error", func(t *testing.T) {
		assert.False(t, client.IsHostAlive("", types.AnySuccessOrRedirect))
	})
}

func TestProbeTimeoutBoundsTheWholeProbe(t *testing.T) {
	const healthEndpointDelay = 300 * time.Millisecond
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(healthEndpointDelay)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	t.Run("a slow answer within the timeout is a successful Probe", func(t *testing.T) {
		client := NewHttpClient(time.Minute, 2*time.Second)
		assert.True(t, client.IsHostAlive(slow.URL, types.AnySuccessOrRedirect))
	})

	t.Run("an answer past the timeout is a failed Probe, returned at the timeout", func(t *testing.T) {
		client := NewHttpClient(time.Minute, 50*time.Millisecond)
		start := time.Now()
		assert.False(t, client.IsHostAlive(slow.URL, types.AnySuccessOrRedirect))
		assert.Less(t, time.Since(start), healthEndpointDelay)
	})
}

func TestProbeTimeoutBoundsTheDial(t *testing.T) {
	const probeTimeout = 50 * time.Millisecond
	var dialBudget atomic.Int64
	dialThatHangsUntilItsBudgetRunsOut := func(_ string, timeout time.Duration) (net.Conn, error) {
		dialBudget.Store(int64(timeout))
		time.Sleep(timeout)
		return nil, fasthttp.ErrDialTimeout
	}
	client := newProbeClient(dialThatHangsUntilItsBudgetRunsOut, probeTimeout)

	assert.False(t, client.IsHostAlive("http://backend.test:8080/health", types.AnySuccessOrRedirect))
	assert.Positive(t, dialBudget.Load(), "the dial is bounded")
	assert.LessOrEqual(t, time.Duration(dialBudget.Load()), probeTimeout, "the dial only gets what is left of the Probe timeout")
}
