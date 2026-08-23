package server

import (
	"net"
	"time"

	"go.uber.org/zap"
)

// tcpKeepaliveListener enables TCP keepalive, with the configured period, on
// every accepted connection. It wraps the shared listener so the setting
// applies whichever stack serves it: fasthttp only honors its own keepalive
// fields and net/http only configures listeners it creates itself.
type tcpKeepaliveListener struct {
	net.Listener
	period time.Duration
}

func withTCPKeepalive(ln net.Listener, period time.Duration) net.Listener {
	if period <= 0 {
		return ln
	}
	return tcpKeepaliveListener{Listener: ln, period: period}
}

func (l tcpKeepaliveListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Best-effort: both stacks treat a non-nil Accept error as fatal, so a
		// failed setsockopt on one connection must not stop the server. A
		// connection whose fd is already dead fails on first read instead.
		if err := tcpConn.SetKeepAlive(true); err != nil {
			zap.S().Warnf("Enabling TCP keepalive for %s failed: %v", conn.RemoteAddr().String(), err)
		} else if err := tcpConn.SetKeepAlivePeriod(l.period); err != nil {
			zap.S().Warnf("Setting TCP keepalive period for %s failed: %v", conn.RemoteAddr().String(), err)
		}
	}
	return conn, nil
}
