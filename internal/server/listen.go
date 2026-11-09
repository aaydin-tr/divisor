package server

import (
	"context"
	"net"
)

// Listen binds address exclusively: a port another process holds is an error,
// never shared. Go would bind an IPv4 wildcard on "tcp" dual-stack, so an IPv4 host stays tcp4.
func Listen(address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(context.Background(), networkOf(address), address)
}

func networkOf(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "tcp"
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return "tcp4"
	}
	return "tcp"
}
