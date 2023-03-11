package config

import (
	"fmt"
	"testing"
	"time"

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
		config := Config{Backends: []Backend{{}}}
		err := config.PrepareConfig()
		assert.EqualError(t, err, "Please choose valid port")
	})

	t.Run("default round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "", Port: "8000"}
		err := config.PrepareConfig()
		assert.Nil(t, err)
		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("is valid type", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "test", Port: "8000"}
		err := config.PrepareConfig()
		assert.EqualError(t, err, fmt.Sprintf("Please choose valid load balancing type e.g %v", ValidTypes))
	})

	t.Run("w-round-robin to round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "w-round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("default HealtCheckerTime", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000", HealthCheckerTime: -1}
		err := config.PrepareConfig()
		assert.Nil(t, err)
		assert.Equal(t, DefaultHealtCheckerTime, config.HealthCheckerTime)
	})

	t.Run("default monitoring host and port", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.Equal(t, "localhost", config.Monitoring.Host)
		assert.Equal(t, "8001", config.Monitoring.Port)
	})

	t.Run("custom headers", func(t *testing.T) {
		customHeaders := map[string]string{
			"test": "test",
		}
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000", CustomHeaders: customHeaders}
		err := config.PrepareConfig()

		assert.EqualError(t, err, fmt.Sprintf("Please choose valid custom header, e.g %v", ValidCustomHeaders))
	})

	t.Run("default funcs", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000"}
		err := config.PrepareConfig()

		assert.Nil(t, err)
		assert.NotNil(t, config.HashFunc)
		assert.NotNil(t, config.HealthCheckerFunc)
	})

	t.Run("prepareServer return error", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000", Server: Server{HttpVersion: Http2}}
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
			Weight: 0,
		}}, Type: "w-round-robin", Port: "8000"}

		err := config.prepareBackends()
		fmt.Println(err)
		assert.EqualError(t, err, "When using the weighted-round-robin algorithm, a weight must be specified for each backend.")
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
	})

	t.Run("http2 without cert and key file", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)
		basic.Server.HttpVersion = Http2
		err = basic.Server.prepareServer()

		assert.EqualError(t, err, "The HTTP/2 connection can be only established if the server is using TLS. Please provide cert and key file")
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
			MaxIdleWorkerDuration:         time.Second,
			TCPKeepalivePeriod:            time.Second,
			Concurrency:                   1,
			ReadTimeout:                   time.Second,
			WriteTimeout:                  time.Second,
			IdleTimeout:                   time.Second,
			DisableKeepalive:              true,
			DisableHeaderNamesNormalizing: true,
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
		assert.Equal(t, basic.Server.DisableKeepalive, true)
		assert.Equal(t, basic.Server.DisableHeaderNamesNormalizing, true)
	})
}
