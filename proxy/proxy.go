package proxy

import (
	"net"

	"github.com/aaydin-tr/balancer/pkg/config"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/valyala/fasthttp"
)

// Hop-by-hop headers. These are removed when sent to the backend.
// As of RFC 7230, hop-by-hop headers are required to appear in the
// Connection header field. These are the headers defined by the
// obsoleted RFC 2616 (section 13.5.1) and are used for backward
// compatibility.
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection", // non-standard but still sent by libcurl and rejected by e.g. google
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",      // canonicalized version of "TE"
	"Trailer", // not Trailers per URL above; https://www.rfc-editor.org/errata_search.php?eid=4522
	"Transfer-Encoding",
	"Upgrade",
}

type ProxyClient struct {
	proxy *fasthttp.HostClient
	Addr  string
}

func (h *ProxyClient) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
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

	return nil
}

func (h *ProxyClient) preReq(req *fasthttp.Request, clientIP net.IP, host []byte) {
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	//TODO
	// req.SetHost(helper.B2s(host))
	// req.SetRequestURI(helper.B2s(req.RequestURI()))
	req.Header.Set("X-Forwarded-For", clientIP.String())
}

func (h *ProxyClient) postRes(res *fasthttp.Response) {
	for _, h := range hopHeaders {
		res.Header.Del(h)
	}
}

func (h *ProxyClient) serverError(res *fasthttp.Response, err string) {
	for _, h := range hopHeaders {
		res.Header.Del(h)
	}

	res.SetStatusCode(fasthttp.StatusInternalServerError)
	res.SetConnectionClose()
	res.Header.Set("Content-Type", "application/json")
	res.SetBody(helper.S2b(`{"message":"` + err + `"}`))
}

// TODO
func (h *ProxyClient) setCustomHeaders() {

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

	return &ProxyClient{proxy: proxyClient, Addr: backend.Addr}
}
