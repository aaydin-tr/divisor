package http

import (
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/valyala/fasthttp"
)

type HttpClient struct {
	client       *fasthttp.Client
	probeTimeout time.Duration
}

// NewHttpClient's probeTimeout bounds each whole Probe (dial, write and read)
// with one deadline.
func NewHttpClient(dnsCacheDuration, probeTimeout time.Duration) *HttpClient {
	return newProbeClient(newProbeDialer(dnsCacheDuration).DialTimeout, probeTimeout)
}

func newProbeClient(dialBackend fasthttp.DialFuncWithTimeout, probeTimeout time.Duration) *HttpClient {
	return &HttpClient{
		client: &fasthttp.Client{
			MaxIdleConnDuration: 5 * time.Second,
			MaxConnWaitTimeout:  30 * time.Second,
			// DialTimeout, not Dial: fasthttp hands it what is left of the
			// DoTimeout deadline, so dialing counts against probeTimeout.
			DialTimeout: dialBackend,
		},
		probeTimeout: probeTimeout,
	}
}

func newProbeDialer(dnsCacheDuration time.Duration) *fasthttp.TCPDialer {
	return &fasthttp.TCPDialer{
		Concurrency:      4096,
		DNSCacheDuration: dnsCacheDuration,
	}
}

// IsHostAlive runs one Probe; a redirect answer is judged as is, not followed.
func (h *HttpClient) IsHostAlive(url string, expectedStatus types.ExpectedProbeStatus) bool {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.Header.SetMethod(fasthttp.MethodGet)
	resp := fasthttp.AcquireResponse()
	err := h.client.DoTimeout(req, resp, h.probeTimeout)

	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if err != nil {
		return false
	}
	return expectedStatus.IsSatisfiedBy(resp.StatusCode())
}
