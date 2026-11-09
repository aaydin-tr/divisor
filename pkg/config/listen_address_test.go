package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListenAddressesOfAnIPv6Host(t *testing.T) {
	for _, host := range []string{"::1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			cfg := &Config{Host: host, Port: "8080", Monitoring: Monitoring{Host: host, Port: "8001"}}

			assert.Equal(t, "[::1]:8080", cfg.GetAddr())
			assert.Equal(t, "[::1]:8001", cfg.GetMonitoringAddr())
			assert.Equal(t, "http://[::1]:8080", cfg.GetURL())
		})
	}
}

func TestListenAddressesOfAnIPv4HostAndAHostname(t *testing.T) {
	cfg := &Config{Host: "0.0.0.0", Port: "8080", Monitoring: Monitoring{Host: "localhost", Port: "8001"}}

	assert.Equal(t, "0.0.0.0:8080", cfg.GetAddr())
	assert.Equal(t, "localhost:8001", cfg.GetMonitoringAddr())
	assert.Equal(t, "http://0.0.0.0:8080", cfg.GetURL())
}
