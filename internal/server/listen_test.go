package server

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListenRefusesAnAddressAnotherListenerHolds(t *testing.T) {
	first, err := Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer first.Close()

	second, err := Listen(first.Addr().String())
	if err == nil {
		second.Close()
	}
	require.Error(t, err, "a second listener must not share the port")
}

func TestListenBindsAnIPv6Address(t *testing.T) {
	listener, err := Listen("[::1]:0")
	if err != nil && !hasIPv6Loopback() {
		t.Skipf("no IPv6 loopback on this machine: %s", err)
	}
	require.NoError(t, err)
	defer listener.Close()

	conn, err := net.Dial("tcp6", listener.Addr().String())
	require.NoError(t, err)
	conn.Close()
}

func TestListenOnTheIPv4WildcardAcceptsNoIPv6Clients(t *testing.T) {
	if !hasIPv6Loopback() {
		t.Skip("no IPv6 loopback on this machine")
	}
	listener, err := Listen("0.0.0.0:0")
	require.NoError(t, err)
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	conn, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", port), time.Second)
	if err == nil {
		conn.Close()
	}
	require.Error(t, err, "host 0.0.0.0 must stay IPv4-only")
}

func hasIPv6Loopback() bool {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	listener.Close()
	return true
}
