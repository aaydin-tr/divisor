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

var protocolRegex = regexp.MustCompile(`(^https?://)`)

type Backend struct {
	URL                       string        `yaml:"url"`
	Weight                    uint          `yaml:"weight,omitempty"`
	MaxConnection             int           `yaml:"max_conn,omitempty"`
	MaxConnWaitTimeout        time.Duration `yaml:"max_conn_timeout,omitempty"`
	MaxConnDuration           time.Duration `yaml:"max_conn_duration,omitempty"`
	MaxIdleConnDuration       time.Duration `yaml:"max_idle_conn_duration,omitempty"`
	MaxIdemponentCallAttempts int           `yaml:"max_idemponent_call_attempts,omitempty"`
}

type Config struct {
	Type     string    `yaml:"type"`
	Port     string    `yaml:"port"`
	Backends []Backend `yaml:"backends"`
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

func PrepareConfig(config *Config) *Config {
	if len(config.Backends) == 0 {
		log.Fatal("At least one backend must be set")
		return nil
	}

	if config.Port == "" {
		log.Fatal("Please choose valid port")
		return nil
	}

	if config.Type == "" {
		config.Type = "round-robin"
	}

	if !helper.Contains(ValidTypes, config.Type) {
		log.Fatal("Please choose valid load balancing type")
		return nil
	}

	for i := 0; i < len(config.Backends); i++ {
		b := &config.Backends[i]
		b.URL = protocolRegex.ReplaceAllString(b.URL, "")

		if config.Type == "w-round-robin" && b.Weight <= 0 {
			log.Fatal("When using the weighted-round-robin algorithm, a weight must be specified for each backend.")
			return nil
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

	if config.Type == "w-round-robin" && len(config.Backends) == 1 {
		config.Type = "round-robin"
	}

	return config
}
