//go:build darwin || linux

package server

import (
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func socketOption(t *testing.T, conn net.Conn, level, option int) int {
	t.Helper()
	rawConn, err := conn.(*net.TCPConn).SyscallConn()
	require.NoError(t, err)
	var value int
	require.NoError(t, rawConn.Control(func(fd uintptr) {
		value, err = syscall.GetsockoptInt(int(fd), level, option)
	}))
	require.NoError(t, err)
	return value
}

func TestTCPKeepaliveListenerEnablesKeepaliveOnAcceptedConnections(t *testing.T) {
	ln := withTCPKeepalive(localListener(t), 10*time.Second)
	defer ln.Close()

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer client.Close()

	accepted, err := ln.Accept()
	require.NoError(t, err)
	defer accepted.Close()

	assert.NotZero(t, socketOption(t, accepted, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE), "SO_KEEPALIVE must be on for the accepted connection")
	// Go enables keepalive by default with a 15s idle time; the configured
	// period is what proves the wrapper ran. The option is in seconds.
	assert.Equal(t, 10, socketOption(t, accepted, syscall.IPPROTO_TCP, tcpKeepaliveIdleOption))
}

func TestWithTCPKeepaliveIsANoOpWithoutAPeriod(t *testing.T) {
	ln := localListener(t)
	defer ln.Close()
	assert.Equal(t, ln, withTCPKeepalive(ln, 0), "no period configured: the listener is returned as is")
}
