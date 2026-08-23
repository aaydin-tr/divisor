package http

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

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
	client := NewHttpClient()
	assert.IsType(t, client, &HttpClient{})
	assert.IsType(t, client.client, &fasthttp.Client{})
}

func TestIsHostAlive(t *testing.T) {
	client := HttpClient{client: &fasthttp.Client{}}
	handler := &statusServer{}
	server := httptest.NewServer(handler)
	defer server.Close()

	alive := map[int]bool{
		200: true, 201: true, 204: true, 299: true,
		301: true, 302: true, 304: true, 399: true,
		400: false, 401: false, 404: false, 500: false, 503: false,
	}
	for status, wantAlive := range alive {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			handler.status.Store(int32(status))
			assert.Equal(t, wantAlive, client.IsHostAlive(server.URL))
		})
	}

	t.Run("redirect is not followed", func(t *testing.T) {
		var hits int32
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}))
		defer redirecting.Close()
		assert.True(t, client.IsHostAlive(redirecting.URL))
		assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
	})

	t.Run("error", func(t *testing.T) {
		status := client.IsHostAlive("")
		assert.False(t, status)
	})
}
