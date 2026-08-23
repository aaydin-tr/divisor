package server

import (
	"net"
	"time"
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
		if err := tcpConn.SetKeepAlive(true); err != nil {
			conn.Close()
			return nil, err
		}
		if err := tcpConn.SetKeepAlivePeriod(l.period); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}
