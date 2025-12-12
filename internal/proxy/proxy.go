package proxy

import (
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

	var serverErr error
	if err := h.proxy.Do(req, res); err != nil {
		serverErr = err
	}

	if h.middlewareExecutor != nil {
		if handledErr := h.middlewareExecutor.RunOnResponse(mwCtx, serverErr); handledErr != nil {
			h.postRes(res)
			return handledErr
		}
	}

	h.postRes(res)
	if serverErr != nil {
		h.serverError(res, serverErr.Error())
		return serverErr
	}

	atomic.AddUint64(h.totalResTime, uint64(time.Since(s).Milliseconds()))
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

func (h *ProxyClient) serverError(res *fasthttp.Response, err string) {
	zap.S().Infof("error when proxying the request: %s", err)
	res.SetStatusCode(fasthttp.StatusInternalServerError)
	res.SetConnectionClose()
	res.Header.Set("Content-Type", "application/json")
	res.SetBody(helper.S2B(`{"message":"` + err + `"}`))
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

func (h *ProxyClient) AvgResponseTime() float64 {
	rc := atomic.LoadUint64(h.totalRequestCount)
	rt := atomic.LoadUint64(h.totalResTime)
	if rc == 0 || rt == 0 {
		return 0
	}

	return float64(rt) / float64(rc)
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
	}

	return &ProxyClient{
		proxy:              proxyClient,
		Addr:               backend.Url,
		addrB:              helper.S2B(backend.Url),
		totalRequestCount:  new(uint64),
		totalResTime:       new(uint64),
		customHeaders:      customHeaders,
		middlewareExecutor: middlewareExecutor,
	}
}
