package http

import (
	"time"

	"github.com/valyala/fasthttp"
)

type HttpClient struct {
	client *fasthttp.Client
}

func NewHttpClient() *HttpClient {
	return &HttpClient{client: &fasthttp.Client{
		ReadTimeout:         5 * time.Second,
		WriteTimeout:        5 * time.Second,
		MaxIdleConnDuration: 1 * time.Hour,
		MaxConnWaitTimeout:  30 * time.Second,
		Dial: (&fasthttp.TCPDialer{
			Concurrency:      4096,
			DNSCacheDuration: time.Hour,
		}).Dial,
	}}
}

func (h *HttpClient) HealtChecker(url string) int {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.Header.SetMethod(fasthttp.MethodGet)
	resp := fasthttp.AcquireResponse()
	err := h.client.Do(req, resp)

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err != nil {
		return 0
	}
	return resp.StatusCode()
}
