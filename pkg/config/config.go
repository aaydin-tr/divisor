package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/http"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

var (
	ErrAtLeastOneBackend     = errors.New("At least one backend must be set")
	ErrInvalidPort           = errors.New("Please choose valid port")
	ErrInvalidWeight         = errors.New("When using the weighted-round-robin algorithm, a weight must be specified for each backend")
	ErrHttp2WithoutTls       = errors.New("The HTTP/2 connection can be only established if the server is using TLS. Please provide cert and key file")
	ErrInvalidTLSKeyPair     = errors.New("cert_file/key_file could not be loaded as a TLS key pair")
	ErrBackendUrlEmpty       = errors.New("Backend url must not be empty")
	ErrBackendUrlInvalid     = errors.New("Backend url is not valid")
	ErrBackendUrlHttps       = errors.New("Backend url must not use https, divisor terminates TLS and always speaks plain HTTP to backends")
	ErrBackendUrlScheme      = errors.New("Backend url has an unsupported scheme")
	ErrBackendUrlUserinfo    = errors.New("Backend url must not contain userinfo")
	ErrBackendUrlNotHostPort = errors.New("Backend url must be host:port only, divisor cannot forward to a path")
	ErrBackendUrlNoHost      = errors.New("Backend url has no host")
)

var ValidTypes = []string{"round-robin", "w-round-robin", "ip-hash", "random", "least-connection", "least-response-time"}
var ValidCustomHeaders = []string{"$remote_addr", "$time", "$uuid", "$incremental"}

const (
	DefaultMaxConnection             = 512
	DefaultMaxConnWaitTimeout        = time.Second * 30
	DefaultMaxConnDuration           = time.Second * 10
	DefaultMaxIdleConnDuration       = time.Second * 10
	DefaultMaxIdemponentCallAttempts = 5

	DefaultHealthCheckerTime = time.Second * 30

	// No "unlimited" setting exists; see docs/adr/0003-bounded-proxy-timeout.md.
	DefaultProxyTimeout = time.Second * 60

	DefaultMaxRequestBodySize = fasthttp.DefaultMaxRequestBodySize

	Http1 = "http1.1"
	Http2 = "http2"

	DefaultMaxIdleWorkerDuration = 10 * time.Second
)

type Middleware struct {
	Name     string         `yaml:"name"`
	Disabled bool           `yaml:"disabled"`
	Code     string         `yaml:"code,omitempty"`
	File     string         `yaml:"file,omitempty"`
	Config   map[string]any `yaml:"config,omitempty"`
}

type Backend struct {
	Url                       string        `yaml:"url"`
	HealthCheckPath           string        `yaml:"health_check_path"`
	Weight                    uint          `yaml:"weight,omitempty"`
	MaxConnection             int           `yaml:"max_conn"`
	MaxConnWaitTimeout        time.Duration `yaml:"max_conn_timeout"`
	MaxConnDuration           time.Duration `yaml:"max_conn_duration"`
	MaxIdleConnDuration       time.Duration `yaml:"max_idle_conn_duration"`
	MaxIdemponentCallAttempts int           `yaml:"max_idemponent_call_attempts"`
	// Copied from the global server.proxy_timeout by PrepareConfig.
	ProxyTimeout time.Duration `yaml:"-"`
}

func (b *Backend) GetHealthCheckURL() string {
	return "http://" + b.Url + b.HealthCheckPath
}

type Monitoring struct {
	Host string `yaml:"host"`
	Port string `yaml:"port"`
}

type Server struct {
	HttpVersion           string        `yaml:"http_version"`
	CertFile              string        `yaml:"cert_file"`
	KeyFile               string        `yaml:"key_file"`
	MaxIdleWorkerDuration time.Duration `yaml:"max_idle_worker_duration"`
	TCPKeepalivePeriod    time.Duration `yaml:"tcp_keepalive_period"`
	Concurrency           int           `yaml:"concurrency"`
	ReadTimeout           time.Duration `yaml:"read_timeout"`
	WriteTimeout          time.Duration `yaml:"write_timeout"`
	IdleTimeout           time.Duration `yaml:"idle_timeout"`
	ProxyTimeout          time.Duration `yaml:"proxy_timeout"`
	MaxRequestBodySize    int           `yaml:"max_request_body_size"`
	DisableKeepalive      bool          `yaml:"disable_keepalive"`
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
	Server            Server        `yaml:"server"`
	Middlewares       []Middleware  `yaml:"middlewares"`
	HealthCheckerTime time.Duration `yaml:"health_checker_time"`
}

func (c *Config) GetAddr() string {
	return c.Host + ":" + c.Port
}

func (c *Config) GetMonitoringAddr() string {
	return c.Monitoring.Host + ":" + c.Monitoring.Port
}

func (c *Config) GetURL() string {
	schema := "http"
	if c.Server.KeyFile != "" && c.Server.CertFile != "" {
		schema = "https"
	}
	return schema + "://" + c.Host + ":" + c.Port
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
		return ErrAtLeastOneBackend
	}

	if c.Host == "" {
		c.Host = "localhost"
	}

	if c.Port == "" {
		return ErrInvalidPort
	}

	if c.Type == "" {
		c.Type = "round-robin"
	}

	if !helper.Contains(ValidTypes, c.Type) {
		return fmt.Errorf("Please choose valid load balancing type e.g %v", ValidTypes)
	}

	if c.Type == "w-round-robin" && len(c.Backends) == 1 {
		c.Type = "round-robin"
	}

	if c.HealthCheckerTime <= 0 {
		c.HealthCheckerTime = DefaultHealthCheckerTime
	}

	if c.Monitoring.Host == "" {
		c.Monitoring.Host = "localhost"
	}

	if c.Monitoring.Port == "" {
		c.Monitoring.Port = "8001"
	}

	for _, value := range c.CustomHeaders {
		if !helper.Contains(ValidCustomHeaders, value) {
			return fmt.Errorf("Please choose valid custom header, e.g %v", ValidCustomHeaders)
		}
	}

	if err := c.validateMiddlewares(); err != nil {
		return err
	}

	// Default funcs
	// TODO make more flexible
	c.HashFunc = helper.HashFunc
	c.HealthCheckerFunc = http.NewHttpClient().IsHostAlive

	err := c.Server.prepareServer()
	if err != nil {
		return err
	}

	return c.prepareBackends()
}

func (c *Config) prepareBackends() error {
	for i := 0; i < len(c.Backends); i++ {
		b := &c.Backends[i]

		addr, err := normalizeBackendAddress(b.Url)
		if err != nil {
			return err
		}
		b.Url = addr

		if c.Type == "w-round-robin" && b.Weight <= 0 {
			return ErrInvalidWeight
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

		b.ProxyTimeout = c.Server.ProxyTimeout
	}

	return nil
}

// normalizeBackendAddress derives the dialable Backend address (host:port)
// from the config url: an optional http:// scheme and a bare trailing slash
// are stripped, a missing port defaults to 80. Anything divisor cannot honor
// (path, query, userinfo) is rejected, as is https://
// (docs/adr/0004-plaintext-only-backends.md).
func normalizeBackendAddress(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrBackendUrlEmpty
	}

	withScheme := raw
	if !strings.Contains(raw, "://") {
		withScheme = "http://" + raw
	}

	u, err := url.Parse(withScheme)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrBackendUrlInvalid, raw, err)
	}

	if u.Scheme == "https" {
		return "", fmt.Errorf("%w: %q", ErrBackendUrlHttps, raw)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("%w: %q", ErrBackendUrlScheme, raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: %q", ErrBackendUrlUserinfo, raw)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q", ErrBackendUrlNotHostPort, raw)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("%w: %q", ErrBackendUrlNoHost, raw)
	}

	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "80"), nil
	}
	return u.Host, nil
}

func (s *Server) prepareServer() error {
	if s.HttpVersion == "" || s.HttpVersion != Http2 {
		s.HttpVersion = Http1
	}

	if s.HttpVersion == Http2 && (s.CertFile == "" || s.KeyFile == "") {
		return ErrHttp2WithoutTls
	}

	if err := helper.IsFileExist(s.CertFile); err != nil && s.CertFile != "" {
		return err
	}

	if err := helper.IsFileExist(s.KeyFile); err != nil && s.KeyFile != "" {
		return err
	}

	// Parse the pair now: an unloadable pair would otherwise only surface
	// when the listener starts, after every other startup step succeeded.
	if s.CertFile != "" && s.KeyFile != "" {
		if _, err := tls.LoadX509KeyPair(s.CertFile, s.KeyFile); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidTLSKeyPair, err)
		}
	}

	if s.MaxIdleWorkerDuration == 0 {
		s.MaxIdleWorkerDuration = DefaultMaxIdleWorkerDuration
	}

	if s.Concurrency == 0 {
		s.Concurrency = fasthttp.DefaultConcurrency
	}

	// Zero means the default, not unlimited (ADR 0003).
	if s.ProxyTimeout <= 0 {
		s.ProxyTimeout = DefaultProxyTimeout
	}

	if s.MaxRequestBodySize <= 0 {
		s.MaxRequestBodySize = DefaultMaxRequestBodySize
	}

	return nil
}

func (c *Config) validateMiddlewares() error {
	for i, mw := range c.Middlewares {
		if mw.Name == "" {
			return fmt.Errorf("middleware at index %d: name is required", i)
		}
		if !mw.Disabled {
			if mw.Code == "" && mw.File == "" {
				return fmt.Errorf("middleware '%s': either code or file must be specified", mw.Name)
			}
			if mw.Code != "" && mw.File != "" {
				return fmt.Errorf("middleware '%s': cannot specify both code and file", mw.Name)
			}
		}
	}
	return nil
}
