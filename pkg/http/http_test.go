package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type mockServer struct {
	isCalled bool
}

func (m *mockServer) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if m.isCalled {
		res.WriteHeader(400)
		return
	}
	m.isCalled = true
	res.WriteHeader(200)
}

func TestNewHttpClient(t *testing.T) {
	client := NewHttpClient()
	assert.IsType(t, client, &HttpClient{})
	assert.IsType(t, client.client, &fasthttp.Client{})
}

func TestIsHostAlive(t *testing.T) {
	client := HttpClient{client: &fasthttp.Client{}}
	handler := mockServer{}
	server := httptest.NewServer(&handler)
	defer server.Close()

	t.Run("200", func(t *testing.T) {
		status := client.IsHostAlive(server.URL)
		assert.True(t, status)
	})

	t.Run("400", func(t *testing.T) {
		status := client.IsHostAlive(server.URL)
		assert.False(t, status)
	})

	t.Run("error", func(t *testing.T) {
		status := client.IsHostAlive("")
		assert.False(t, status)
	})
}
