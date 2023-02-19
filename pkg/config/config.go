package config

import (
	"os"
	"regexp"
	"time"

	"github.com/aaydin-tr/balancer/core/types"
	"github.com/aaydin-tr/balancer/pkg/helper"
	"github.com/aaydin-tr/balancer/pkg/http"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

var ValidTypes = []string{"round-robin", "w-round-robin", "ip-hash", "random"}
var ValidCustomHeaders = []string{"$remote_addr", "$time", "$uuid", "$incremental"}

const DefaultMaxConnection = 512
const DefaultMaxConnWaitTimeout = time.Second * 30
const DefaultMaxConnDuration = time.Second * 10
const DefaultMaxIdleConnDuration = time.Second * 10
const DefaultMaxIdemponentCallAttempts = 5

const DefaultHealtCheckerTime = time.Second * 30

var protocolRegex = regexp.MustCompile(`(^https?://)`)

type Backend struct {
	Url                       string        `yaml:"url"`
	Weight                    uint          `yaml:"weight,omitempty"`
	MaxConnection             int           `yaml:"max_conn,omitempty"`
	MaxConnWaitTimeout        time.Duration `yaml:"max_conn_timeout,omitempty"`
	MaxConnDuration           time.Duration `yaml:"max_conn_duration,omitempty"`
	MaxIdleConnDuration       time.Duration `yaml:"max_idle_conn_duration,omitempty"`
	MaxIdemponentCallAttempts int           `yaml:"max_idemponent_call_attempts,omitempty"`
}

func (b *Backend) GetURL() string {
	return "http://" + b.Url
}

type Monitoring struct {
	Host string `yaml:"host,omitempty"`
	Port string `yaml:"port,omitempty"`
}

type Config struct {
	CustomHeaders    map[string]string `yaml:"custom_headers"`
	Monitoring       Monitoring        `yaml:"monitoring"`
	Type             string            `yaml:"type"`
	Host             string            `yaml:"host"`
	Port             string            `yaml:"port"`
	Backends         []Backend         `yaml:"backends"`
	HealtCheckerTime time.Duration     `yaml:"healt_checker_time"`
	HealtCheckerFunc types.HealtCheckerFunc
	HashFunc         types.HashFunc
}

func (c *Config) GetAddr() string {
	return c.Host + ":" + c.Port
}

func (c *Config) GetMonitoringAddr() string {
	return c.Monitoring.Host + ":" + c.Monitoring.Port
}

func ParseConfigFile(path string) *Config {
	configFile, err := os.ReadFile(path)
	if err != nil {
		zap.S().Error(err)
	}

	var config Config
	err = yaml.Unmarshal(configFile, &config)

	if err != nil {
		zap.S().Error(err)
	}

	return &config
}

func (c *Config) PrepareConfig() {
	zap.S().Info("Parsing config file")

	if len(c.Backends) == 0 {
		zap.S().Error("At least one backend must be set")
		return
	}

	if c.Host == "" {
		c.Host = "localhost"
	}

	if c.Port == "" {
		zap.S().Error("Please choose valid port")
		return
	}

	if c.Type == "" {
		c.Type = "round-robin"
	}

	if !helper.Contains(ValidTypes, c.Type) {
		zap.S().Error("Please choose valid load balancing type")
		return
	}

	if c.Type == "w-round-robin" && len(c.Backends) == 1 {
		c.Type = "round-robin"
	}

	if c.HealtCheckerTime <= 0 {
		c.HealtCheckerTime = DefaultHealtCheckerTime
	}

	if c.Monitoring.Host == "" {
		c.Monitoring.Host = "localhost"
	}

	if c.Monitoring.Port == "" {
		c.Monitoring.Port = "8001"
	}

	for _, value := range c.CustomHeaders {
		if !helper.Contains(ValidCustomHeaders, value) {
			zap.S().Error("Please choose valid custom header, e.g ", ValidCustomHeaders)
		}
		return
	}

	// Default funcs
	// TODO make more flexible
	c.HashFunc = helper.HashFunc
	c.HealtCheckerFunc = http.NewHttpClient().DefaultHealtChecker

	zap.S().Info("Config file parse successfully")
	c.prepareBackends()
	zap.S().Info("Default configurations applied")
}

func (c *Config) prepareBackends() {
	for i := 0; i < len(c.Backends); i++ {
		b := &c.Backends[i]
		b.Url = protocolRegex.ReplaceAllString(b.Url, "")

		if c.Type == "w-round-robin" && b.Weight <= 0 {
			zap.S().Error("When using the weighted-round-robin algorithm, a weight must be specified for each backend.")
			return
		}

		if b.MaxConnection <= 0 {
			b.MaxConnection = DefaultMaxConnection
		}

		if b.MaxConnWaitTimeout <= 0 {
			b.MaxConnWaitTimeout = DefaultMaxConnWaitTimeout
		}

		if b.MaxConnDuration <= 0 {
			b.MaxConnDuration = DefaultMaxConnDuration
		}

		if b.MaxIdleConnDuration <= 0 {
			b.MaxIdleConnDuration = DefaultMaxIdleConnDuration
		}

		if b.MaxIdemponentCallAttempts <= 0 {
			b.MaxIdemponentCallAttempts = DefaultMaxIdemponentCallAttempts
		}
	}
}
