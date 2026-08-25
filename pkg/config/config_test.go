package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaydin-tr/divisor/internal/testcert"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestParseConfigFile(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)

		addr := basic.GetAddr()
		assert.Equal(t, ":8000", addr)

		mAddr := basic.GetMonitoringAddr()
		assert.Equal(t, ":", mAddr)
	})

	t.Run("config file not found", func(t *testing.T) {
		basic, err := ParseConfigFile("config.yaml")
		assert.Nil(t, basic)
		assert.Error(t, err)
	})

	t.Run("config file not parsable", func(t *testing.T) {
		basic, err := ParseConfigFile("test.yaml")
		assert.Nil(t, basic)
		assert.Error(t, err)
	})

}

func TestPrepareConfig(t *testing.T) {
	t.Parallel()

	t.Run("zero backend", func(t *testing.T) {
		config := Config{}
		err := config.PrepareConfig()
		assert.EqualError(t, err, "At least one backend must be set")
	})

	t.Run("default localhost", func(t *testing.T) {
		basic, _ := ParseConfigFile("../../examples/basic.config.yaml")
		err := basic.PrepareConfig()
		assert.Nil(t, err)
		assert.Equal(t, "localhost", basic.Host)
	})

	t.Run("port is required", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}}
		err := config.PrepareConfig()
		assert.EqualError(t, err, "Please choose valid port")
	})

	t.Run("default round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "", Port: "8000"}
		err := config.PrepareConfig()
		assert.Nil(t, err)
		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("is valid type", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "test", Port: "8000"}
		err := config.PrepareConfig()
		assert.EqualError(t, err, fmt.Sprintf("Please choose valid load balancing type e.g %v", ValidTypes))
	})

	t.Run("w-round-robin to round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "w-round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("default HealthCheckerTime", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "round-robin", Port: "8000", HealthCheckerTime: -1}
		err := config.PrepareConfig()
		assert.Nil(t, err)
		assert.Equal(t, DefaultHealthCheckerTime, config.HealthCheckerTime)
	})

	t.Run("default monitoring host and port", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.Equal(t, "localhost", config.Monitoring.Host)
		assert.Equal(t, "8001", config.Monitoring.Port)
	})

	t.Run("custom headers", func(t *testing.T) {
		customHeaders := map[string]string{
			"test": "test",
		}
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "round-robin", Port: "8000", CustomHeaders: customHeaders}
		err := config.PrepareConfig()

		assert.EqualError(t, err, fmt.Sprintf("Please choose valid custom header, e.g %v", ValidCustomHeaders))
	})

	t.Run("default funcs", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.NotNil(t, config.HashFunc)
		assert.NotNil(t, config.HealthCheckerFunc)
	})

	t.Run("prepareServer return error", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Type: "round-robin", Port: "8000", Server: Server{HttpVersion: Http2}}
		err := config.PrepareConfig()

		assert.NotNil(t, err)
	})
}

func TestPrepareBackends(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)

		basic.prepareBackends()

		for _, b := range basic.Backends {
			assert.Equal(t, b.MaxConnection, DefaultMaxConnection)
			assert.Equal(t, b.MaxConnWaitTimeout, DefaultMaxConnWaitTimeout)
			assert.Equal(t, b.MaxConnDuration, DefaultMaxConnDuration)
			assert.Equal(t, b.MaxIdleConnDuration, DefaultMaxIdleConnDuration)
			assert.Equal(t, b.MaxIdemponentCallAttempts, DefaultMaxIdemponentCallAttempts)
		}
	})

	t.Run("GetHealthCheckURL", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)

		basic.prepareBackends()

		for _, b := range basic.Backends {
			url := b.GetHealthCheckURL()
			assert.Equal(t, "http://"+b.Url+"/", url)

		}
	})

	t.Run("set values", func(t *testing.T) {
		config := Config{Backends: []Backend{{
			Url:                       "localhost:8080",
			MaxConnection:             1,
			MaxConnWaitTimeout:        time.Duration(1),
			MaxConnDuration:           time.Duration(1),
			MaxIdleConnDuration:       time.Duration(1),
			MaxIdemponentCallAttempts: 5,
		}}, Type: "round-robin", Port: "8000"}

		config.prepareBackends()

		for _, b := range config.Backends {
			assert.Equal(t, b.MaxConnection, 1)
			assert.Equal(t, b.MaxConnWaitTimeout, time.Duration(1))
			assert.Equal(t, b.MaxConnDuration, time.Duration(1))
			assert.Equal(t, b.MaxIdleConnDuration, time.Duration(1))
			assert.Equal(t, b.MaxIdemponentCallAttempts, 5)
		}
	})

	t.Run("w-round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{
			Url:    "localhost:8080",
			Weight: 0,
		}}, Type: "w-round-robin", Port: "8000"}

		err := config.prepareBackends()
		assert.EqualError(t, err, ErrInvalidWeight.Error())
	})

	t.Run("the same address twice is two backends", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}, {Url: "localhost:8080"}}, Type: "round-robin", Port: "8000"}

		err := config.PrepareConfig()
		assert.Nil(t, err)
		assert.Len(t, config.Backends, 2)
	})

}

func TestNormalizeBackendAddress(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name string
		raw  string
		want string
	}{
		{"bare host:port", "localhost:8080", "localhost:8080"},
		{"http scheme stripped", "http://localhost:8080", "localhost:8080"},
		{"bare trailing slash tolerated", "http://localhost:8080/", "localhost:8080"},
		{"trailing slash without scheme", "localhost:8080/", "localhost:8080"},
		{"missing port defaults to 80", "localhost", "localhost:80"},
		{"scheme and missing port", "http://127.0.0.1", "127.0.0.1:80"},
		{"ipv6 with port", "[::1]:9000", "[::1]:9000"},
		{"ipv6 missing port", "[::1]", "[::1]:80"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBackendAddress(tc.raw)
			assert.Nil(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"https rejected", "https://api.example.com"},
		{"https with port rejected", "https://api.example.com:8443"},
		{"unsupported scheme", "ftp://host:21"},
		{"path", "http://localhost:8080/api"},
		{"double slash is a path", "http://localhost:8080//"},
		{"query", "http://localhost:8080?x=1"},
		{"fragment", "http://localhost:8080#frag"},
		{"userinfo", "http://user:pass@localhost:8080"},
		{"invalid port", "localhost:80a0"},
		{"no host", "http://"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBackendAddress(tc.raw)
			assert.Error(t, err)
			assert.Equal(t, "", got)
		})
	}

	t.Run("errors match their sentinels", func(t *testing.T) {
		_, err := normalizeBackendAddress("https://api.example.com")
		assert.ErrorIs(t, err, ErrBackendUrlHttps)
		assert.ErrorContains(t, err, "terminates TLS")

		_, err = normalizeBackendAddress("")
		assert.ErrorIs(t, err, ErrBackendUrlEmpty)

		_, err = normalizeBackendAddress("ftp://host:21")
		assert.ErrorIs(t, err, ErrBackendUrlScheme)

		_, err = normalizeBackendAddress("http://user:pass@localhost:8080")
		assert.ErrorIs(t, err, ErrBackendUrlUserinfo)

		_, err = normalizeBackendAddress("localhost:80a0")
		assert.ErrorIs(t, err, ErrBackendUrlInvalid)

		_, err = normalizeBackendAddress("http://")
		assert.ErrorIs(t, err, ErrBackendUrlNoHost)
	})

	t.Run("PrepareConfig surfaces the error", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "http://localhost:8080/api"}}, Port: "8000"}
		err := config.PrepareConfig()
		assert.ErrorIs(t, err, ErrBackendUrlNotHostPort)
	})
}

func TestPrepareServer(t *testing.T) {
	t.Parallel()

	t.Run("default values", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)

		basic.Server.prepareServer()

		assert.Equal(t, basic.Server.HttpVersion, Http1)
		assert.Equal(t, basic.Server.MaxIdleWorkerDuration, DefaultMaxIdleWorkerDuration)
		assert.Equal(t, basic.Server.Concurrency, fasthttp.DefaultConcurrency)
		assert.Equal(t, basic.Server.ProxyTimeout, DefaultProxyTimeout)
		assert.Equal(t, basic.Server.MaxRequestBodySize, DefaultMaxRequestBodySize)
	})

	t.Run("zero proxy_timeout and max_request_body_size mean the default, not unlimited", func(t *testing.T) {
		server := Server{ProxyTimeout: 0, MaxRequestBodySize: 0}
		err := server.prepareServer()

		assert.Nil(t, err)
		assert.Equal(t, server.ProxyTimeout, DefaultProxyTimeout)
		assert.Equal(t, server.MaxRequestBodySize, DefaultMaxRequestBodySize)
	})

	t.Run("proxy_timeout is copied onto every backend", func(t *testing.T) {
		config := Config{
			Backends: []Backend{{Url: "localhost:2000"}, {Url: "localhost:3000"}},
			Port:     "8000",
			Server:   Server{ProxyTimeout: 5 * time.Second},
		}

		err := config.PrepareConfig()

		assert.Nil(t, err)
		for _, b := range config.Backends {
			assert.Equal(t, b.ProxyTimeout, 5*time.Second)
		}
	})

	t.Run("http2 without cert and key file", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)
		basic.Server.HttpVersion = Http2
		err = basic.Server.prepareServer()

		assert.EqualError(t, err, ErrHttp2WithoutTls.Error())
	})

	t.Run("cert file does not exist", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)
		basic.Server.CertFile = "testcert"
		err = basic.Server.prepareServer()

		assert.EqualError(t, err, fmt.Sprintf("%s file does not exist", "testcert"))
	})

	t.Run("key file does not exist", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)
		basic.Server.KeyFile = "testkey"
		err = basic.Server.prepareServer()

		assert.EqualError(t, err, fmt.Sprintf("%s file does not exist", "testkey"))
	})

	t.Run("set values", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")

		server := Server{
			MaxIdleWorkerDuration: time.Second,
			TCPKeepalivePeriod:    time.Second,
			Concurrency:           1,
			ReadTimeout:           time.Second,
			WriteTimeout:          time.Second,
			IdleTimeout:           time.Second,
			ProxyTimeout:          2 * time.Second,
			MaxRequestBodySize:    1024,
			DisableKeepalive:      true,
		}
		basic.Server = server
		err = basic.Server.prepareServer()

		assert.Nil(t, err)
		assert.Equal(t, basic.Server.MaxIdleWorkerDuration, time.Second)
		assert.Equal(t, basic.Server.TCPKeepalivePeriod, time.Second)
		assert.Equal(t, basic.Server.Concurrency, 1)
		assert.Equal(t, basic.Server.ReadTimeout, time.Second)
		assert.Equal(t, basic.Server.WriteTimeout, time.Second)
		assert.Equal(t, basic.Server.IdleTimeout, time.Second)
		assert.Equal(t, basic.Server.ProxyTimeout, 2*time.Second)
		assert.Equal(t, basic.Server.MaxRequestBodySize, 1024)
		assert.Equal(t, basic.Server.DisableKeepalive, true)
	})
}

func TestPrepareServerParsesTLSKeyPair(t *testing.T) {
	t.Run("a loadable pair passes", func(t *testing.T) {
		certPath, keyPath, err := testcert.Write(t.TempDir())
		assert.NoError(t, err)
		server := Server{CertFile: certPath, KeyFile: keyPath}
		assert.NoError(t, server.prepareServer())
	})

	t.Run("files that exist but are not a key pair fail at config time", func(t *testing.T) {
		dir := t.TempDir()
		certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
		assert.NoError(t, os.WriteFile(certPath, []byte("not a certificate"), 0o600))
		assert.NoError(t, os.WriteFile(keyPath, []byte("not a key"), 0o600))
		server := Server{CertFile: certPath, KeyFile: keyPath}
		err := server.prepareServer()
		assert.ErrorIs(t, err, ErrInvalidTLSKeyPair)
	})

	t.Run("a mismatched pair fails at config time", func(t *testing.T) {
		certPath, _, err := testcert.Write(t.TempDir())
		assert.NoError(t, err)
		_, otherKey, err := testcert.Write(t.TempDir())
		assert.NoError(t, err)
		server := Server{CertFile: certPath, KeyFile: otherKey}
		assert.ErrorIs(t, server.prepareServer(), ErrInvalidTLSKeyPair)
	})
}

func TestPrepareLogging(t *testing.T) {
	t.Parallel()

	t.Run("defaults to json at info with the access log off", func(t *testing.T) {
		logging := Logging{}
		assert.NoError(t, logging.prepareLogging())
		assert.Equal(t, LoggingFormatJSON, logging.Format)
		assert.Equal(t, DefaultLoggingLevel, logging.Level)
		assert.False(t, logging.AccessLog)
	})

	t.Run("access_log parses under strict decoding", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		yaml := "port: \"8000\"\nbackends:\n  - url: localhost:8080\nlogging:\n  access_log: true\n"
		assert.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
		config, err := ParseConfigFile(path)
		assert.NoError(t, err)
		assert.NoError(t, config.PrepareConfig())
		assert.True(t, config.Logging.AccessLog)
	})

	t.Run("valid values pass unchanged", func(t *testing.T) {
		for _, format := range []string{LoggingFormatJSON, LoggingFormatConsole} {
			for _, level := range []string{"debug", "info", "warn", "error", "fatal"} {
				logging := Logging{Format: format, Level: level}
				assert.NoError(t, logging.prepareLogging())
				assert.Equal(t, format, logging.Format)
				assert.Equal(t, level, logging.Level)
			}
		}
	})

	t.Run("an invalid format fails naming the value", func(t *testing.T) {
		logging := Logging{Format: "logfmt"}
		err := logging.prepareLogging()
		assert.ErrorIs(t, err, ErrInvalidLoggingFormat)
		assert.Contains(t, err.Error(), `"logfmt"`)
	})

	t.Run("an invalid level fails naming the value", func(t *testing.T) {
		logging := Logging{Level: "verbose"}
		err := logging.prepareLogging()
		assert.ErrorIs(t, err, ErrInvalidLoggingLevel)
		assert.Contains(t, err.Error(), `"verbose"`)
	})

	t.Run("PrepareConfig validates the logging section", func(t *testing.T) {
		config := Config{
			Backends: []Backend{{Url: "localhost:8080"}},
			Port:     "8000",
			Logging:  Logging{Format: "logfmt"},
		}
		assert.ErrorIs(t, config.PrepareConfig(), ErrInvalidLoggingFormat)
	})

	t.Run("PrepareConfig defaults the logging section", func(t *testing.T) {
		config := Config{Backends: []Backend{{Url: "localhost:8080"}}, Port: "8000"}
		assert.NoError(t, config.PrepareConfig())
		assert.Equal(t, LoggingFormatJSON, config.Logging.Format)
		assert.Equal(t, DefaultLoggingLevel, config.Logging.Level)
	})
}
