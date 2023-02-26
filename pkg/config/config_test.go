package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
		observedZapCore, observedLogs := observer.New(zap.InfoLevel)
		observedLogger := zap.New(observedZapCore)
		zap.ReplaceGlobals(observedLogger)

		config := Config{}
		config.PrepareConfig()

		log := observedLogs.All()[1]
		assert.Equal(t, "At least one backend must be set", log.Message)
	})

	t.Run("default localhost", func(t *testing.T) {
		basic, _ := ParseConfigFile("../../examples/basic.config.yaml")
		basic.PrepareConfig()
		assert.Equal(t, "localhost", basic.Host)
	})

	t.Run("port is required", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.InfoLevel)
		observedLogger := zap.New(observedZapCore)
		zap.ReplaceGlobals(observedLogger)

		config := Config{Backends: []Backend{{}}}
		config.PrepareConfig()

		log := observedLogs.All()[1]
		assert.Equal(t, "Please choose valid port", log.Message)
	})

	t.Run("default round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "", Port: "8000"}
		config.PrepareConfig()
		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("is valid type", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.InfoLevel)
		observedLogger := zap.New(observedZapCore)
		zap.ReplaceGlobals(observedLogger)

		config := Config{Backends: []Backend{{}}, Type: "test", Port: "8000"}
		config.PrepareConfig()

		log := observedLogs.All()[1]
		assert.Equal(t, "Please choose valid load balancing type", log.Message)
	})

	t.Run("w-round-robin to round-robin", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "w-round-robin", Port: "8000"}
		config.PrepareConfig()

		assert.Equal(t, "round-robin", config.Type)
	})

	t.Run("default HealtCheckerTime", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000", HealthCheckerTime: -1}
		config.PrepareConfig()

		assert.Equal(t, DefaultHealtCheckerTime, config.HealthCheckerTime)
	})

	t.Run("default monitoring host and port", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000"}
		config.PrepareConfig()

		assert.Equal(t, "localhost", config.Monitoring.Host)
		assert.Equal(t, "8001", config.Monitoring.Port)
	})

	t.Run("custom headers", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.InfoLevel)
		observedLogger := zap.New(observedZapCore)
		zap.ReplaceGlobals(observedLogger)

		customHeaders := map[string]string{
			"test": "test",
		}
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000", CustomHeaders: customHeaders}
		config.PrepareConfig()

		log := observedLogs.All()[1]
		assert.Equal(t, "Please choose valid custom header, e.g [$remote_addr $time $uuid $incremental]", log.Message)
	})

	t.Run("default funcs", func(t *testing.T) {
		config := Config{Backends: []Backend{{}}, Type: "round-robin", Port: "8000"}
		config.PrepareConfig()

		assert.NotNil(t, config.HashFunc)
		assert.NotNil(t, config.HealthCheckerFunc)
	})
}

func TestPrepareBackends(t *testing.T) {
	t.Parallel()

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

	t.Run("GetURL", func(t *testing.T) {
		basic, err := ParseConfigFile("../../examples/basic.config.yaml")
		assert.Equal(t, "round-robin", basic.Type)
		assert.Nil(t, err)

		basic.prepareBackends()

		for _, b := range basic.Backends {
			url := b.GetURL()
			assert.Equal(t, "http://"+b.Url, url)

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
		observedZapCore, observedLogs := observer.New(zap.InfoLevel)
		observedLogger := zap.New(observedZapCore)
		zap.ReplaceGlobals(observedLogger)

		config := Config{Backends: []Backend{{
			Weight: 0,
		}}, Type: "w-round-robin", Port: "8000"}

		config.prepareBackends()

		log := observedLogs.All()[0]
		assert.Equal(t, "When using the weighted-round-robin algorithm, a weight must be specified for each backend.", log.Message)
	})

}
