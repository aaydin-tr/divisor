package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
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
	RecentResponseTime() float64
	ResetRecentResponseTime()
	Close() error
}

// Hop-by-hop headers, removed in both directions. RFC 9110 §7.6.1 makes the
// Connection header the authority on which headers are hop-by-hop (see
// delConnectionNominated); this is the RFC 2616 §13.5.1 list, kept for
// backward compatibility with peers that do not nominate them.
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

type errorMessage struct {
	Message string `json:"message"`
}

// Weight of the newest sample in the response-time moving average. Low enough
// that one slow response cannot steer traffic away from a Backend for long,
// high enough that a Backend turning slow is noticed within a few requests.
const responseTimeSmoothing = 0.2

// A failed request is scored as at least this slow. Scored by real elapsed
// time instead, a Backend refusing connections in microseconds would rank as
// the fastest in the pool and draw every request until the next Probe round.
const failureResponseTimePenalty = 10 * time.Second

// Response times accumulate in microseconds and are reported in milliseconds.
const microsPerMilli = float64(1000)

const (
	badGatewayMessage     = `{"message":"bad gateway"}`
	gatewayTimeoutMessage = `{"message":"gateway timeout"}`
)

type ProxyClient struct {
	proxy                *fasthttp.HostClient
	totalRequestCount    *uint64
	totalResTime         *uint64
	measuredRequestCount *uint64
	recentResTime        *uint64
	customHeaders        map[string]string
	middlewareExecutor   *middlewarePkg.Executor
	Addr                 string
	addrB                []byte
	proxyTimeout         time.Duration
}

func (h *ProxyClient) ReverseProxyHandler(ctx *fasthttp.RequestCtx) error {
	requestSequenceNumber := atomic.AddUint64(h.totalRequestCount, 1)
	s := time.Now()

	req := &ctx.Request
	res := &ctx.Response
	clientIP := helper.S2B(ctx.RemoteIP().String())
	mwCtx := middleware.NewContext(ctx)

	h.preReq(req, clientIP, requestSequenceNumber)

	if h.middlewareExecutor != nil {
		if err := h.middlewareExecutor.RunOnRequest(mwCtx); err != nil {
			h.postRes(res)
			if isShortCircuit(err) {
				return nil
			}
			middlewareError(res, err)
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
	// Scored here, before OnResponse: the score is the Backend's, whatever a
	// Middleware does with its response afterwards.
	if serverErr != nil {
		h.recordFailure(time.Since(s))
	} else {
		h.recordResponseTime(time.Since(s))
	}

	if h.middlewareExecutor != nil {
		if err := h.middlewareExecutor.RunOnResponse(mwCtx, serverErr); err != nil {
			h.postRes(res)
			if isShortCircuit(err) {
				return serverErr
			}
			middlewareError(res, err)
			return err
		}
	}

	h.postRes(res)
	if serverErr != nil {
		h.serverError(res, serverErr)
	}
	return serverErr
}

// isShortCircuit reports whether a Middleware answered the request itself
// (CONTEXT.md: Short-circuit); ctx.Response then goes out as it left it.
func isShortCircuit(err error) bool {
	if !errors.Is(err, middleware.ErrShortCircuit) {
		return false
	}
	zap.S().Debugf("middleware short-circuited the request: %s", err)
	return true
}

// recordResponseTime keeps two measures of a successful request: a lifetime
// total for the stats endpoint and a moving average for least-response-time.
// Microseconds: sub-millisecond Backends must not all average to zero, or
// least-response-time cannot tell them apart.
func (h *ProxyClient) recordResponseTime(elapsed time.Duration) {
	atomic.AddUint64(h.totalResTime, uint64(elapsed.Microseconds()))
	atomic.AddUint64(h.measuredRequestCount, 1)
	h.storeRecentResTime(durationMicros(elapsed))
}

// recordFailure feeds only the moving average: a failure must push the
// Backend's score above healthy peers, not skew the lifetime average of
// successful requests. Timeouts keep their real (larger) elapsed time.
func (h *ProxyClient) recordFailure(elapsed time.Duration) {
	h.storeRecentResTime(durationMicros(max(elapsed, failureResponseTimePenalty)))
}

func durationMicros(d time.Duration) float64 {
	return float64(d) / float64(time.Microsecond)
}

func (h *ProxyClient) storeRecentResTime(micros float64) {
	for {
		current := atomic.LoadUint64(h.recentResTime)
		next := micros
		if current != 0 {
			next = responseTimeSmoothing*micros + (1-responseTimeSmoothing)*math.Float64frombits(current)
		}
		if atomic.CompareAndSwapUint64(h.recentResTime, current, math.Float64bits(next)) {
			return
		}
	}
}

func (h *ProxyClient) preReq(req *fasthttp.Request, clientIP []byte, requestSequenceNumber uint64) {
	// Nominated headers go first: a client nominating Host or X-Forwarded-For
	// only deletes its own values, and divisor's are set below.
	delConnectionNominated(&req.Header)
	for _, h := range hopHeaders {
		req.Header.DelBytes(h)
	}

	req.URI().SetSchemeBytes(httpB)
	req.SetHostBytes(h.addrB)
	req.Header.SetBytesKV(XForwardedFor, clientIP)
	h.setCustomHeaders(req, clientIP, requestSequenceNumber)
}

func (h *ProxyClient) postRes(res *fasthttp.Response) {
	delConnectionNominated(&res.Header)
	for _, h := range hopHeaders {
		res.Header.DelBytes(h)
	}
}

type headerSet interface {
	PeekAll(key string) [][]byte
	DelBytes(key []byte)
}

var contentLengthHeader = []byte(fasthttp.HeaderContentLength)

// Tokens that name no header to delete: "close" and "keep-alive" are
// connection options (Keep-Alive the header is in hopHeaders); Content-Length
// is framing on a streamed body, and ReverseProxy keeps framing intact too.
var connectionOptions = [][]byte{[]byte("close"), []byte("keep-alive"), contentLengthHeader}

// delConnectionNominated removes the headers a Connection header nominates as
// hop-by-hop (RFC 9110 §7.6.1), the way net/http's ReverseProxy does. Header
// names are always normalized on both stacks, so DelBytes matches whatever
// case the peer used.
func delConnectionNominated(h headerSet) {
	for _, value := range h.PeekAll(fasthttp.HeaderConnection) {
		for token := range bytes.SplitSeq(value, helper.S2B(",")) {
			if token = bytes.TrimSpace(token); len(token) > 0 && !isConnectionOption(token) {
				h.DelBytes(token)
			}
		}
	}
}

func isConnectionOption(token []byte) bool {
	for _, option := range connectionOptions {
		if bytes.EqualFold(token, option) {
			return true
		}
	}
	return false
}

// middlewareError answers a Middleware failure. Whatever the response held —
// a Backend's reply, an earlier Middleware's edits — is discarded: a failed
// Middleware's response cannot be trusted, and an untouched pooled response
// would otherwise go out as an empty 200 OK.
func middlewareError(res *fasthttp.Response, err error) {
	zap.S().Infof("middleware returned an error: %s", err)
	res.Reset()
	res.SetStatusCode(fasthttp.StatusInternalServerError)
	res.Header.Set("Content-Type", "application/json")
	res.SetBody(errorMessageBody(err))
}

func errorMessageBody(err error) []byte {
	body, marshalErr := json.Marshal(errorMessage{Message: err.Error()})
	if marshalErr != nil {
		return helper.S2B(`{"message":"internal error"}`)
	}

	return body
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
// an unreachable Backend is Down, not hanging. The underlying error names
// Backend addresses and dial details, so it is logged, not sent.
func (h *ProxyClient) serverError(res *fasthttp.Response, err error) {
	zap.S().Infof("error when proxying the request: %s", err)
	status, message := fasthttp.StatusBadGateway, badGatewayMessage
	if errors.Is(err, fasthttp.ErrTimeout) {
		status, message = fasthttp.StatusGatewayTimeout, gatewayTimeoutMessage
	}
	res.SetStatusCode(status)
	res.SetConnectionClose()
	res.Header.Set("Content-Type", "application/json")
	res.SetBodyString(message)
}

func (h *ProxyClient) setCustomHeaders(req *fasthttp.Request, clientIP []byte, requestSequenceNumber uint64) {
	for k, v := range h.customHeaders {
		switch v {
		case "$remote_addr":
			req.Header.SetBytesV(k, clientIP)
		case "$time":
			req.Header.Set(k, time.Now().Local().Format("2006-01-02T15:04:05.000Z"))
		case "$incremental":
			req.Header.Set(k, strconv.FormatUint(requestSequenceNumber, 10))
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

// AvgResponseTime returns the lifetime average over successful requests in
// milliseconds; totalResTime accumulates microseconds. Failed and in-flight
// requests count toward totalRequestCount but not here — dividing by them
// would drag the average down.
func (h *ProxyClient) AvgResponseTime() float64 {
	rc := atomic.LoadUint64(h.measuredRequestCount)
	if rc == 0 {
		return 0
	}

	return float64(atomic.LoadUint64(h.totalResTime)) / microsPerMilli / float64(rc)
}

// RecentResponseTime returns a moving average of the last response times in
// milliseconds, or 0 while the Backend is unmeasured. Unlike the lifetime
// average it decays, and failures are scored as slow, so a failing Backend
// loses traffic instead of keeping its last healthy score.
func (h *ProxyClient) RecentResponseTime() float64 {
	return math.Float64frombits(atomic.LoadUint64(h.recentResTime)) / microsPerMilli
}

// ResetRecentResponseTime makes the Backend read as unmeasured again. Called
// on Rejoin: the score from before it went Down — possibly a failure
// penalty — would otherwise starve it long after it recovered.
func (h *ProxyClient) ResetRecentResponseTime() {
	atomic.StoreUint64(h.recentResTime, 0)
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
		proxy:                proxyClient,
		Addr:                 backend.Url,
		addrB:                helper.S2B(backend.Url),
		totalRequestCount:    new(uint64),
		totalResTime:         new(uint64),
		measuredRequestCount: new(uint64),
		recentResTime:        new(uint64),
		customHeaders:        customHeaders,
		middlewareExecutor:   middlewareExecutor,
		proxyTimeout:         backend.ProxyTimeout,
	}
}
