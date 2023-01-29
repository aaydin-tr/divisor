package config

import (
	"log"
	"os"
	"regexp"
	"time"

	"github.com/aaydin-tr/balancer/pkg/helper"
	"gopkg.in/yaml.v3"
)

var ValidTypes = []string{"round-robin", "w-round-robin", "ip-hash", "random"}

const DefaultMaxConnection = 512
const DefaultMaxConnWaitTimeout = time.Second * 30
const DefaultMaxConnDuration = time.Minute * 5
const DefaultMaxIdleConnDuration = time.Minute * 5
const DefaultMaxIdemponentCallAttempts = 5

const DefaultHealtCheckerTime = time.Second * 30

var protocolRegex = regexp.MustCompile(`(^https?://)`)

type Backend struct {
	Addr                      string        `yaml:"url"`
	Weight                    uint          `yaml:"weight,omitempty"`
	MaxConnection             int           `yaml:"max_conn,omitempty"`
	MaxConnWaitTimeout        time.Duration `yaml:"max_conn_timeout,omitempty"`
	MaxConnDuration           time.Duration `yaml:"max_conn_duration,omitempty"`
	MaxIdleConnDuration       time.Duration `yaml:"max_idle_conn_duration,omitempty"`
	MaxIdemponentCallAttempts int           `yaml:"max_idemponent_call_attempts,omitempty"`
}

func (b *Backend) GetURL() string {
	return "http://" + b.Addr
}

type Config struct {
	Type             string        `yaml:"type"`
	Port             string        `yaml:"port"`
	Backends         []Backend     `yaml:"backends"`
	HealtCheckerTime time.Duration `yaml:"healt_checker_time"`
}

func ParseConfigFile(path string) *Config {
	configFile, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	var config Config
	err = yaml.Unmarshal(configFile, &config)

	if err != nil {
		log.Fatal(err)
	}

	return &config
}

func (c *Config) PrepareConfig() {
	if len(c.Backends) == 0 {
		log.Fatal("At least one backend must be set")
		return
	}

	if c.Port == "" {
		log.Fatal("Please choose valid port")
		return
	}

	if c.Type == "" {
		c.Type = "round-robin"
	}

	if !helper.Contains(ValidTypes, c.Type) {
		log.Fatal("Please choose valid load balancing type")
		return
	}

	if c.Type == "w-round-robin" && len(c.Backends) == 1 {
		c.Type = "round-robin"
	}

	if c.HealtCheckerTime <= 0 {
		c.HealtCheckerTime = DefaultHealtCheckerTime
	}

	c.prepareBackends()
}

func (c *Config) prepareBackends() {
	for i := 0; i < len(c.Backends); i++ {
		b := &c.Backends[i]
		b.Addr = protocolRegex.ReplaceAllString(b.Addr, "")

		if c.Type == "w-round-robin" && b.Weight <= 0 {
			log.Fatal("When using the weighted-round-robin algorithm, a weight must be specified for each backend.")
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
