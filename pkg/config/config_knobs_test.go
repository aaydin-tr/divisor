package config

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The client server accepts outside traffic, so its host defaults to 0.0.0.0;
// monitoring exposure stays opt-in behind the localhost default.
func TestDefaultBindAddresses(t *testing.T) {
	t.Parallel()

	config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Port: "8000"}
	assert.NoError(t, config.PrepareConfig())
	assert.Equal(t, "0.0.0.0", config.Host)
	assert.Equal(t, "localhost", config.Monitoring.Host)
}

func TestGracefulShutdownTimeout(t *testing.T) {
	t.Parallel()

	t.Run("unset means the default", func(t *testing.T) {
		server := Server{}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, DefaultGracefulShutdownTimeout, server.GracefulShutdownTimeout)
	})

	t.Run("a set value passes unchanged", func(t *testing.T) {
		server := Server{GracefulShutdownTimeout: 5 * time.Second}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, 5*time.Second, server.GracefulShutdownTimeout)
	})

	t.Run("a negative value fails naming the key", func(t *testing.T) {
		server := Server{GracefulShutdownTimeout: -time.Second}
		err := server.prepareServer()
		assert.ErrorIs(t, err, ErrInvalidGracefulShutdownTimeout)
		assert.Contains(t, err.Error(), "server.graceful_shutdown_timeout")
	})
}

func TestHeaderBufferSizes(t *testing.T) {
	t.Parallel()

	t.Run("unset or negative means the default", func(t *testing.T) {
		for _, size := range []int{0, -1} {
			server := Server{ReadBufferSize: size, WriteBufferSize: size}
			assert.NoError(t, server.prepareServer())
			assert.Equal(t, DefaultReadBufferSize, server.ReadBufferSize)
			assert.Equal(t, DefaultWriteBufferSize, server.WriteBufferSize)
		}
	})

	t.Run("set values pass unchanged", func(t *testing.T) {
		server := Server{ReadBufferSize: 16384, WriteBufferSize: 8192}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, 16384, server.ReadBufferSize)
		assert.Equal(t, 8192, server.WriteBufferSize)
	})
}

func TestConnectionCaps(t *testing.T) {
	t.Parallel()

	t.Run("unset or negative means unlimited", func(t *testing.T) {
		for _, cap := range []int{0, -1} {
			server := Server{MaxConnsPerIP: cap, MaxRequestsPerConn: cap}
			assert.NoError(t, server.prepareServer())
			assert.Equal(t, DefaultMaxConnsPerIP, server.MaxConnsPerIP)
			assert.Equal(t, DefaultMaxRequestsPerConn, server.MaxRequestsPerConn)
		}
	})

	t.Run("set values pass unchanged", func(t *testing.T) {
		server := Server{MaxConnsPerIP: 100, MaxRequestsPerConn: 1000}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, 100, server.MaxConnsPerIP)
		assert.Equal(t, 1000, server.MaxRequestsPerConn)
	})
}

func TestTLSMinVersion(t *testing.T) {
	t.Parallel()

	t.Run("unset keeps the runtime default", func(t *testing.T) {
		server := Server{}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, uint16(0), server.TLSMinVersionValue())
	})

	t.Run("1.2 maps to tls.VersionTLS12", func(t *testing.T) {
		server := Server{TLSMinVersion: "1.2"}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, uint16(tls.VersionTLS12), server.TLSMinVersionValue())
	})

	t.Run("1.3 maps to tls.VersionTLS13", func(t *testing.T) {
		server := Server{TLSMinVersion: "1.3"}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, uint16(tls.VersionTLS13), server.TLSMinVersionValue())
	})

	for _, rejected := range []string{"1.1", "1.0", "tls1.2", "13", "TLSv1.3"} {
		t.Run("rejects "+rejected, func(t *testing.T) {
			server := Server{TLSMinVersion: rejected}
			err := server.prepareServer()
			assert.ErrorIs(t, err, ErrInvalidTLSMinVersion)
			assert.Contains(t, err.Error(), rejected)
		})
	}

	t.Run("an unquoted YAML 1.2 still parses as a string", func(t *testing.T) {
		path := writeConfig(t, "port: \"8000\"\nbackends:\n  - url: localhost:8080\nserver:\n  tls_min_version: 1.2\n")
		config, err := ParseConfigFile(path)
		assert.NoError(t, err)
		assert.NoError(t, config.PrepareConfig())
		assert.Equal(t, uint16(tls.VersionTLS12), config.Server.TLSMinVersionValue())
	})
}

func TestDNSCacheDuration(t *testing.T) {
	t.Parallel()

	t.Run("unset means the default", func(t *testing.T) {
		server := Server{}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, DefaultDNSCacheDuration, server.DNSCacheDuration)
	})

	t.Run("a set value passes unchanged", func(t *testing.T) {
		server := Server{DNSCacheDuration: 2 * time.Second}
		assert.NoError(t, server.prepareServer())
		assert.Equal(t, 2*time.Second, server.DNSCacheDuration)
	})

	t.Run("a negative value fails naming the key", func(t *testing.T) {
		server := Server{DNSCacheDuration: -time.Second}
		err := server.prepareServer()
		assert.ErrorIs(t, err, ErrInvalidDNSCacheDuration)
		assert.Contains(t, err.Error(), "server.dns_cache_duration")
	})

	t.Run("is copied onto every backend for the proxy dialer", func(t *testing.T) {
		config := Config{
			Backends: []Backend{{Url: "localhost:2000"}, {Url: "localhost:3000"}},
			Port:     "8000",
			Server:   Server{DNSCacheDuration: 2 * time.Second},
		}
		assert.NoError(t, config.PrepareConfig())
		for _, b := range config.Backends {
			assert.Equal(t, 2*time.Second, b.DNSCacheDuration)
		}
	})
}
