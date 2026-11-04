package equivalentsetup

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/aaydin-tr/divisor/benchmark/backend"
	"github.com/aaydin-tr/divisor/benchmark/plan"
)

const requestTimeout = 2 * time.Second

// Collect counts a request that gets no answer in Errors instead of returning it.
func Collect(ctx context.Context, url string, check plan.EquivalentSetupCheck) (Sample, error) {
	client := &fasthttp.Client{MaxConnsPerHost: check.Connections}
	defer client.CloseIdleConnections()

	var (
		sample       = Sample{Errors: map[string]int{}}
		sampleMu     sync.Mutex
		requestsSent atomic.Int64
		connections  sync.WaitGroup
	)
	for range check.Connections {
		connections.Go(func() {
			request, response := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(request)
			defer fasthttp.ReleaseResponse(response)
			for ctx.Err() == nil && requestsSent.Add(1) <= int64(check.Requests) {
				request.SetRequestURI(url)
				err := client.DoTimeout(request, response, requestTimeout)
				sampleMu.Lock()
				if err != nil {
					sample.Errors[err.Error()]++
				} else {
					sample.Answers = append(sample.Answers, Answer{
						Status:       response.StatusCode(),
						AnsweredBy:   string(response.Header.Peek(backend.AnsweredByHeader)),
						ForwardedFor: string(response.Header.Peek(backend.ForwardedForHeader)),
					})
				}
				sampleMu.Unlock()
			}
		})
	}
	connections.Wait()
	return sample, ctx.Err()
}
