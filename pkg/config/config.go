package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/http"
	"gopkg.in/yaml.v3"
)

var ValidTypes = []string{"round-robin", "w-round-robin", "ip-hash", "random", "least-connection"}
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
	HealthCheckPath           string        `yaml:"health_check_path"`
	Weight                    uint          `yaml:"weight,omitempty"`
	MaxConnection             int           `yaml:"max_conn,omitempty"`
	MaxConnWaitTimeout        time.Duration `yaml:"max_conn_timeout,omitempty"`
	MaxConnDuration           time.Duration `yaml:"max_conn_duration,omitempty"`
	MaxIdleConnDuration       time.Duration `yaml:"max_idle_conn_duration,omitempty"`
	MaxIdemponentCallAttempts int           `yaml:"max_idemponent_call_attempts,omitempty"`
}

func (b *Backend) GetHealthCheckURL() string {
	return "http://" + b.Url + b.HealthCheckPath
}

type Monitoring struct {
	Host string `yaml:"host,omitempty"`
	Port string `yaml:"port,omitempty"`
}

type Config struct {
	CustomHeaders     map[string]string `yaml:"custom_headers"`
	HealthCheckerFunc types.IsHostAlive
	HashFunc          types.HashFunc
	Monitoring        Monitoring    `yaml:"monitoring"`
	Type              string        `yaml:"type"`
	Host              string        `yaml:"host"`
	Port              string        `yaml:"port"`
	Backends          []Backend     `yaml:"backends"`
	HealthCheckerTime time.Duration `yaml:"health_checker_time"`
}

func (c *Config) GetAddr() string {
	return c.Host + ":" + c.Port
}

func (c *Config) GetMonitoringAddr() string {
	return c.Monitoring.Host + ":" + c.Monitoring.Port
}

func ParseConfigFile(path string) (*Config, error) {
	configFile, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(configFile, &config)

	if err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *Config) PrepareConfig() error {
	if len(c.Backends) == 0 {
		return errors.New("At least one backend must be set")
	}

	if c.Host == "" {
		c.Host = "localhost"
	}

	if c.Port == "" {
		return errors.New("Please choose valid port")
	}

	if c.Type == "" {
		c.Type = "round-robin"
	}

	if !helper.Contains(ValidTypes, c.Type) {
		return errors.New(fmt.Sprintf("Please choose valid load balancing type e.g %v", ValidTypes))
	}

	if c.Type == "w-round-robin" && len(c.Backends) == 1 {
		c.Type = "round-robin"
	}

	if c.HealthCheckerTime <= 0 {
		c.HealthCheckerTime = DefaultHealtCheckerTime
	}

	if c.Monitoring.Host == "" {
		c.Monitoring.Host = "localhost"
	}

	if c.Monitoring.Port == "" {
		c.Monitoring.Port = "8001"
	}

	for _, value := range c.CustomHeaders {
		if !helper.Contains(ValidCustomHeaders, value) {
			return errors.New(fmt.Sprintf("Please choose valid custom header, e.g %v", ValidCustomHeaders))
		}
	}

	// Default funcs
	// TODO make more flexible
	c.HashFunc = helper.HashFunc
	c.HealthCheckerFunc = http.NewHttpClient().IsHostAlive

	return c.prepareBackends()
}

func (c *Config) prepareBackends() error {
	for i := 0; i < len(c.Backends); i++ {
		b := &c.Backends[i]
		b.Url = protocolRegex.ReplaceAllString(b.Url, "")

		if c.Type == "w-round-robin" && b.Weight <= 0 {
			return errors.New("When using the weighted-round-robin algorithm, a weight must be specified for each backend.")
		}

		if b.HealthCheckPath == "" {
			b.HealthCheckPath = "/"
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

	return nil
}
