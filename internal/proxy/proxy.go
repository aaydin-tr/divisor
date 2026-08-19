package proxy

import (
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/middleware"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/helper"
	middlewarePkg "github.com/aaydin-tr/divisor/pkg/middleware"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type ProxyFunc func(*config.Backend, map[string]string, *middlewarePkg.Executor) IProxyClient

type IProxyClient interface {
	ReverseProxyHandler(ctx *fasthttp.RequestCtx) error
	Stat() types.ProxyStat
	PendingRequests() int
	AvgResponseTime() float64
	Close() error
}

// Hop-by-hop headers. These are removed when sent to the backend.
// As of RFC 7230, hop-by-hop headers are required to appear in the
// Connection header field. These are the headers defined by the
// obsoleted RFC 2616 (section 13.5.1) and are used for backward
// compatibility.
var hopHeaders = [][]byte{
	[]byte("Connection"),
	[]byte("Proxy-Connection"), // non-standard but still sent by libcurl and rejected by e.g. google
	[]byte("Keep-Alive"),
	[]byte("Proxy-Authenticate"),
	[]byte("Proxy-Authorization"),
	[]byte("Te"),      // canonicalized version of "TE"
	[]byte("Trailer"), // not Trailers per URL above; https://www.rfc-editor.org/errata_search.php?eid=4522
	[]byte("Transfer-Encoding"),
	[]byte("Upgrade"),
}
var XForwardedFor = []byte("X-Forwarded-For")
var httpB = []byte("http")

type ProxyClient struct {
	proxy              *fasthttp.HostClient
	totalRequestCount  *uint64
	totalResTime       *uint64
	customHeaders      map[string]string
	middlewareExecutor *middlewarePkg.Executor
	Addr               string
	addrB              []byte
	proxyTimeout       time.Duration
}

func (h *ProxyClient) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	atomic.AddUint64(h.totalRequestCount, 1)
	s := time.Now()

	req := &ctx.Request
	res := &ctx.Response
	clientIP := helper.S2B(ctx.RemoteIP().String())
	mwCtx := middleware.NewContext(ctx)

	h.preReq(req, clientIP)

	if h.middlewareExecutor != nil {
		if err := h.middlewareExecutor.RunOnRequest(mwCtx); err != nil {
			h.postRes(res)
			return err
		}
	}

	// fasthttp treats DoTimeout(0) as already expired, not "no deadline".
	var serverErr error
	if h.proxyTimeout > 0 {
		serverErr = h.proxy.DoTimeout(req, res, h.proxyTimeout)
	} else {
		serverErr = h.proxy.Do(req, res)
	}

	if h.middlewareExecutor != nil {
		if handledErr := h.middlewareExecutor.RunOnResponse(mwCtx, serverErr); handledErr != nil {
			h.postRes(res)
			return handledErr
		}
	}

	h.postRes(res)
	if serverErr != nil {
		h.serverError(res, serverErr)
		return serverErr
	}

	// Microseconds: sub-millisecond Backends must not all average to zero,
	// or least-response-time cannot tell them apart.
	atomic.AddUint64(h.totalResTime, uint64(time.Since(s).Microseconds()))
	return nil
}

func (h *ProxyClient) preReq(req *fasthttp.Request, clientIP []byte) {
	for _, h := range hopHeaders {
		req.Header.DelBytes(h)
	}

	req.URI().SetSchemeBytes(httpB)
	req.SetHostBytes(h.addrB)
	req.Header.SetBytesKV(XForwardedFor, clientIP)
	h.setCustomHeaders(req, clientIP)
}

func (h *ProxyClient) postRes(res *fasthttp.Response) {
	for _, h := range hopHeaders {
		res.Header.DelBytes(h)
	}
}

// NoAliveBackends answers a request that arrived while every Backend is Down:
// 503 until a Probe lets one Rejoin.
func NoAliveBackends(ctx *fasthttp.RequestCtx) {
	ctx.Response.SetStatusCode(fasthttp.StatusServiceUnavailable)
	ctx.Response.SetConnectionClose()
	ctx.Response.Header.Set("Content-Type", "application/json")
	ctx.Response.SetBodyString(`{"message":"no backends available"}`)
}

const bodyTooLargeMessage = `{"message":"request body too large"}`

// ErrorHandler mirrors fasthttp's default request-error handling, except an
// oversized body maps to 413 instead of the generic 400.
func ErrorHandler(ctx *fasthttp.RequestCtx, err error) {
	var smallBuffer *fasthttp.ErrSmallBuffer
	var netErr *net.OpError
	switch {
	case errors.Is(err, fasthttp.ErrBodyTooLarge):
		ctx.Response.Reset()
		ctx.Response.SetStatusCode(fasthttp.StatusRequestEntityTooLarge)
		ctx.Response.Header.Set("Content-Type", "application/json")
		ctx.Response.SetBodyString(bodyTooLargeMessage)
	case errors.As(err, &smallBuffer):
		ctx.Error("Too big request header", fasthttp.StatusRequestHeaderFieldsTooLarge)
	case errors.As(err, &netErr) && netErr.Timeout():
		ctx.Error("Request timeout", fasthttp.StatusRequestTimeout)
	default:
		ctx.Error("Error when parsing request", fasthttp.StatusBadRequest)
	}
}

// 504 means proxy_timeout expired on a hanging Backend, which fasthttp
// reports as ErrTimeout; 502 covers everything else. Dial timeouts stay 502:
// an unreachable Backend is Down, not hanging.
func (h *ProxyClient) serverError(res *fasthttp.Response, err error) {
	zap.S().Infof("error when proxying the request: %s", err)
	status := fasthttp.StatusBadGateway
	if errors.Is(err, fasthttp.ErrTimeout) {
		status = fasthttp.StatusGatewayTimeout
	}
	res.SetStatusCode(status)
	res.SetConnectionClose()
	res.Header.Set("Content-Type", "application/json")
	res.SetBody(helper.S2B(`{"message":"` + err.Error() + `"}`))
}

func (h *ProxyClient) setCustomHeaders(req *fasthttp.Request, clientIP []byte) {
	for k, v := range h.customHeaders {
		switch v {
		case "$remote_addr":
			req.Header.SetBytesV(k, clientIP)
		case "$time":
			req.Header.Set(k, time.Now().Local().Format("2006-01-02T15:04:05.000Z"))
		case "$incremental":
			req.Header.Set(k, strconv.FormatUint(atomic.LoadUint64(h.totalRequestCount), 10))
		case "$uuid":
			req.Header.Set(k, uuid.New().String())
		}
	}
}

func (h *ProxyClient) Stat() types.ProxyStat {
	rc := atomic.LoadUint64(h.totalRequestCount)

	return types.ProxyStat{
		TotalReqCount: rc,
		AvgResTime:    h.AvgResponseTime(),
		Addr:          h.Addr,
		LastUseTime:   h.proxy.LastUseTime(),
		ConnsCount:    h.proxy.ConnsCount(),
	}
}

func (h *ProxyClient) PendingRequests() int {
	return h.proxy.PendingRequests()
}

// AvgResponseTime returns milliseconds; totalResTime accumulates microseconds.
func (h *ProxyClient) AvgResponseTime() float64 {
	rc := atomic.LoadUint64(h.totalRequestCount)
	rt := atomic.LoadUint64(h.totalResTime)
	if rc == 0 || rt == 0 {
		return 0
	}

	return float64(rt) / 1000 / float64(rc)
}

func (h *ProxyClient) Close() error {
	h.proxy.CloseIdleConnections()
	return nil
}

func NewProxyClient(backend *config.Backend, customHeaders map[string]string, middlewareExecutor *middlewarePkg.Executor) IProxyClient {
	if backend == nil {
		return nil
	}

	proxyClient := &fasthttp.HostClient{
		Addr:                      backend.Url,
		MaxConns:                  backend.MaxConnection,
		MaxConnDuration:           backend.MaxConnDuration,
		MaxIdleConnDuration:       backend.MaxIdleConnDuration,
		MaxIdemponentCallAttempts: backend.MaxIdemponentCallAttempts,
		MaxConnWaitTimeout:        backend.MaxConnWaitTimeout,
		// Without a pinned dialer, fasthttp uses proxy_timeout as the
		// per-dial bound, hanging on unreachable Backends instead of
		// failing 502 within DefaultDialTimeout (3s).
		Dial: fasthttp.Dial,
	}

	return &ProxyClient{
		proxy:              proxyClient,
		Addr:               backend.Url,
		addrB:              helper.S2B(backend.Url),
		totalRequestCount:  new(uint64),
		totalResTime:       new(uint64),
		customHeaders:      customHeaders,
		middlewareExecutor: middlewareExecutor,
		proxyTimeout:       backend.ProxyTimeout,
	}
}
