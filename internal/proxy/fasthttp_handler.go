package proxy

import (
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/valyala/fasthttp"
)

// NewFasthttpHandler serves balancer on the HTTP/1.1 listener, bounding a Streamed response's client writes and
// watching its client connection: fasthttp notices a gone client only when a write fails.
func NewFasthttpHandler(balancer types.IBalancer, writeTimeout time.Duration) fasthttp.RequestHandler {
	serve := balancer.Serve()
	return func(ctx *fasthttp.RequestCtx) {
		serve(ctx)
		if stream, isStreamed := streamedBodyOf(&ctx.Response); isStreamed {
			// A write deadline set by the handler replaces server.write_timeout's, so the bound carries it.
			stream.boundClientWrites(ctx.SetWriteDeadline, writeTimeoutExpiresAt(time.Now(), writeTimeout))
			stream.watchClientConn(ctx.Conn())
		}
	}
}

// errClientSentDataMidStream closes the connection without the final chunk:
// the watch consumed the client's bytes, so it cannot serve another request.
var errClientSentDataMidStream = errors.New("client sent data before its Streamed response ended; closing the connection")

// A deadline in the past ends a Read in flight, as net/http aborts its own
// background read.
var longAgo = time.Unix(1, 0)

// watchClientConn ends s when its client disconnects: an HTTP/1.1 client sends
// nothing while its response arrives, so a read returns only once it leaves (or pipelines).
func (s *streamedBody) watchClientConn(conn net.Conn) {
	// fasthttp leaves server.read_timeout armed while the response is written.
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	watch := &clientConnWatch{conn: conn, watchEnded: make(chan struct{})}
	go watch.readUntilClientLeaves(func() { s.endWith(streamEndClientDisconnected, errClientGone) }) //nolint:errcheck
	s.stopWatchingClient = watch.stop
}

type clientConnWatch struct {
	conn           net.Conn
	stopping       atomic.Bool
	clientSentData bool
	watchEnded     chan struct{}
}

func (w *clientConnWatch) readUntilClientLeaves(onClientGone func()) {
	defer close(w.watchEnded)
	var firstByte [1]byte
	n, err := w.conn.Read(firstByte[:])
	w.clientSentData = n > 0
	if n == 0 && err != nil && !w.stopping.Load() {
		onClientGone()
	}
}

// stop must return before fasthttp reads the connection again (CloseWithError
// runs first); it reports whether the watch consumed bytes fasthttp will never see.
func (w *clientConnWatch) stop() (clientSentData bool) {
	w.stopping.Store(true)
	w.conn.SetReadDeadline(longAgo) //nolint:errcheck
	<-w.watchEnded
	w.conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	return w.clientSentData
}
