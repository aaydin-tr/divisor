package proxy

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/valyala/fasthttp"
)

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

type ProxyClient struct {
	proxy             *fasthttp.HostClient
	totalRequestCount *uint64
	totalResTime      *uint64
	Addr              string
}

func (h *ProxyClient) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	atomic.AddUint64(h.totalRequestCount, 1)
	s := time.Now()

	req := &ctx.Request
	res := &ctx.Response
	clientIP := ctx.RemoteIP()

	h.preReq(req, clientIP, ctx.Host())

	if err := h.proxy.Do(req, res); err != nil {
		//TODO
		//ctx.Logger().Printf("error when proxying the request: %s", err)
		h.serverError(res, err.Error())
		return err
	}
	h.postRes(res)

	atomic.AddUint64(h.totalResTime, uint64(time.Since(s).Milliseconds()))
	return nil
}

func (h *ProxyClient) preReq(req *fasthttp.Request, clientIP net.IP, host []byte) {
	for _, h := range hopHeaders {
		req.Header.DelBytes(h)
	}
	//TODO
	// req.SetHost(helper.B2s(host))
	// req.SetRequestURI(helper.B2s(req.RequestURI()))
	req.Header.SetBytesK(XForwardedFor, clientIP.String())
}

func (h *ProxyClient) postRes(res *fasthttp.Response) {
	for _, h := range hopHeaders {
		res.Header.DelBytes(h)
	}
}

func (h *ProxyClient) serverError(res *fasthttp.Response, err string) {
	for _, h := range hopHeaders {
		res.Header.DelBytes(h)
	}

	res.SetStatusCode(fasthttp.StatusInternalServerError)
	res.SetConnectionClose()
	res.Header.Set("Content-Type", "application/json")
	res.SetBody(helper.S2b(`{"message":"` + err + `"}`))
}

// TODO
func (h *ProxyClient) setCustomHeaders() {
}

func (h *ProxyClient) Stat() types.ProxyStat {
	rc := atomic.LoadUint64(h.totalRequestCount)
	rt := atomic.LoadUint64(h.totalResTime)
	avg := float64(0)
	if rc != 0 && rt != 0 {
		avg = float64(rt) / float64(rc)
	}

	return types.ProxyStat{
		TotalReqCount: rc,
		AvgResTime:    avg,
		Addr:          h.Addr,
		LastUseTime:   h.proxy.LastUseTime(),
		ConnsCount:    h.proxy.ConnsCount(),
	}
}

func NewProxyClient(backend config.Backend) *ProxyClient {
	proxyClient := &fasthttp.HostClient{
		Addr:                      backend.Addr,
		MaxConns:                  backend.MaxConnection,
		MaxConnDuration:           backend.MaxConnDuration,
		MaxIdleConnDuration:       backend.MaxConnDuration,
		MaxIdemponentCallAttempts: backend.MaxIdemponentCallAttempts,
		MaxConnWaitTimeout:        backend.MaxConnWaitTimeout,
	}

	return &ProxyClient{proxy: proxyClient, Addr: backend.Addr, totalRequestCount: new(uint64), totalResTime: new(uint64)}
}
