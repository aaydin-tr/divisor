package proxy

import (
	"net"
	"net/http"
	"strconv"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/valyala/fasthttp"
)

type NetHttpAdapter struct {
	Balancer types.IBalancer
}

func NewNetHttpAdapter(balancer types.IBalancer) *NetHttpAdapter {
	return &NetHttpAdapter{Balancer: balancer}
}

func (a *NetHttpAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ctx fasthttp.RequestCtx
	ConvertNetHttpRequestToFastHttpRequest(r, &ctx)

	a.Balancer.Serve()(&ctx)

	ctx.Response.Header.All()(func(k []byte, v []byte) bool {
		w.Header().Set(helper.B2S(k), helper.B2S(v))
		return true
	})
	w.Header().Set("Server", "divisor")

	w.WriteHeader(ctx.Response.StatusCode())
	w.Write(ctx.Response.Body())
}

func ConvertNetHttpRequestToFastHttpRequest(r *http.Request, ctx *fasthttp.RequestCtx) {
	ctx.Request.Header.SetMethod(r.Method)

	if r.RequestURI != "" {
		ctx.Request.SetRequestURI(r.RequestURI)
	} else if r.URL != nil {
		ctx.Request.SetRequestURI(r.URL.RequestURI())
	}

	ctx.Request.Header.SetProtocol(r.Proto)
	ctx.Request.SetHost(r.Host)

	for k, values := range r.Header {
		for i, v := range values {
			if i == 0 {
				ctx.Request.Header.Set(k, v)
			} else {
				ctx.Request.Header.Add(k, v)
			}
		}
	}

	if r.Body != nil {
		ctx.Request.SetBodyStream(r.Body, int(r.ContentLength))
	}

	if r.RemoteAddr != "" {
		addr := parseRemoteAddr(r.RemoteAddr)
		ctx.SetRemoteAddr(addr)
	}

}

func parseRemoteAddr(addr string) net.Addr {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return &net.TCPAddr{IP: net.ParseIP(addr)}
	}
	return &net.TCPAddr{
		IP:   net.ParseIP(host),
		Port: parsePort(port),
	}
}

func parsePort(port string) int {
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return p
}
