package config

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/core/types"
	"github.com/aaydin-tr/divisor/pkg/helper"
	"github.com/aaydin-tr/divisor/pkg/http"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

var (
	ErrAtLeastOneBackend                = errors.New("At least one backend must be set")
	ErrInvalidPort                      = errors.New("Please choose valid port")
	ErrInvalidWeight                    = errors.New("When using the weighted-round-robin algorithm, a weight must be specified for each backend")
	ErrHttp2WithoutTls                  = errors.New("The HTTP/2 connection can be only established if the server is using TLS. Please provide cert and key file")
	ErrInvalidHttpVersion               = errors.New("server.http_version must be http1 or http2")
	ErrConfigFileEmpty                  = errors.New("Config file is empty")
	ErrInvalidTLSKeyPair                = errors.New("cert_file/key_file could not be loaded as a TLS key pair")
	ErrBackendUrlEmpty                  = errors.New("Backend url must not be empty")
	ErrBackendUrlInvalid                = errors.New("Backend url is not valid")
	ErrBackendUrlHttps                  = errors.New("Backend url must not use https, divisor terminates TLS and always speaks plain HTTP to backends")
	ErrBackendUrlScheme                 = errors.New("Backend url has an unsupported scheme")
	ErrBackendUrlUserinfo               = errors.New("Backend url must not contain userinfo")
	ErrBackendUrlNotHostPort            = errors.New("Backend url must be host:port only, divisor cannot forward to a path")
	ErrBackendUrlNoHost                 = errors.New("Backend url has no host")
	ErrMiddlewareNameRequired           = errors.New("middleware name is required")
	ErrMiddlewareCodeAndFileEmpty       = errors.New("middleware needs either code or file")
	ErrMiddlewareCodeAndFileBothSet     = errors.New("middleware cannot specify both code and file, choose one")
	ErrInvalidLoggingFormat             = errors.New("logging.format must be json or console")
	ErrInvalidLoggingLevel              = errors.New("logging.level must be a zap level (debug, info, warn, error, dpanic, panic, fatal)")
	ErrInvalidGracefulShutdownTimeout   = errors.New("server.graceful_shutdown_timeout must not be negative")
	ErrInvalidTLSMinVersion             = errors.New("server.tls_min_version must be 1.2 or 1.3")
	ErrInvalidDNSCacheDuration          = errors.New("server.dns_cache_duration must not be negative")
	ErrInvalidHTTP2MaxConcurrentStreams = errors.New("server.http2.max_concurrent_streams must not exceed 2147483647")
	ErrInvalidHealthCheckerTimeout      = errors.New("health_checker_timeout must not be negative")
	ErrInvalidHealthCheckStatus         = errors.New("health_check_status must be a final HTTP status (200-599)")
	ErrInvalidStreamIdleTimeout         = errors.New("server.stream_idle_timeout must not be negative")
)

const (
	RoundRobinType         = "round-robin"
	WeightedRoundRobinType = "w-round-robin"
	IPHashType             = "ip-hash"
	RandomType             = "random"
	LeastConnectionType    = "least-connection"
	LeastResponseTimeType  = "least-response-time"
)

var ValidTypes = []string{RoundRobinType, WeightedRoundRobinType, IPHashType, RandomType, LeastConnectionType, LeastResponseTimeType}
var ValidCustomHeaders = []string{"$remote_addr", "$time", "$uuid", "$incremental"}

const (
	DefaultMaxConnection             = 512
	DefaultMaxConnWaitTimeout        = time.Second * 30
	DefaultMaxConnDuration           = time.Second * 10
	DefaultMaxIdleConnDuration       = time.Second * 10
	DefaultMaxIdemponentCallAttempts = 5

	DefaultHealthCheckerTime    = time.Second * 30
	DefaultHealthCheckerTimeout = time.Second * 5

	// No "unlimited" setting exists; see development/adr/0003-bounded-proxy-timeout.md.
	DefaultProxyTimeout = time.Second * 60

	// No "unlimited" setting exists either; see development/adr/0007-opt-in-response-streaming.md.
	DefaultStreamIdleTimeout = time.Minute * 5

	DefaultMaxRequestBodySize = fasthttp.DefaultMaxRequestBodySize

	DefaultGracefulShutdownTimeout = time.Second * 30

	// fasthttp's per-connection buffer defaults (it keeps them unexported).
	DefaultReadBufferSize  = 4096
	DefaultWriteBufferSize = 4096

	// Unlimited, fasthttp's own default for both caps.
	DefaultMaxConnsPerIP      = 0
	DefaultMaxRequestsPerConn = 0

	// Bounds how long the proxy dialer and the Probe client reuse a resolved
	// Backend IP; deployments with churning Backend IPs (k8s) set it short.
	DefaultDNSCacheDuration = time.Minute

	// net/http's HTTP/2 server default (it keeps it unexported).
	DefaultHTTP2MaxConcurrentStreams = 250

	TLSMinVersion12 = "1.2"
	TLSMinVersion13 = "1.3"

	Http1 = "http1.1"
	Http2 = "http2"

	LoggingFormatJSON    = "json"
	LoggingFormatConsole = "console"
	DefaultLoggingFormat = LoggingFormatJSON
	DefaultLoggingLevel  = "info"

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
	Url                       string                    `yaml:"url"`
	HealthCheckPath           string                    `yaml:"health_check_path"`
	HealthCheckStatus         types.ExpectedProbeStatus `yaml:"health_check_status"`
	Weight                    uint                      `yaml:"weight,omitempty"`
	MaxConnection             int                       `yaml:"max_conn"`
	MaxConnWaitTimeout        time.Duration             `yaml:"max_conn_timeout"`
	MaxConnDuration           time.Duration             `yaml:"max_conn_duration"`
	MaxIdleConnDuration       time.Duration             `yaml:"max_idle_conn_duration"`
	MaxIdemponentCallAttempts int                       `yaml:"max_idemponent_call_attempts"`
	ProxyTimeout              time.Duration             `yaml:"-"`
	DNSCacheDuration          time.Duration             `yaml:"-"`
	Streaming                 bool                      `yaml:"-"`
	StreamIdleTimeout         time.Duration             `yaml:"-"`
}

func (b *Backend) GetHealthCheckURL() string {
	return "http://" + b.Url + b.HealthCheckPath
}

type Monitoring struct {
	Host string `yaml:"host"`
	Port string `yaml:"port"`
}

// Logging is deliberately a section, not top-level keys: future logging
// options (file sink, sampling) belong here (.scratch/logging/spec.md).
type Logging struct {
	Format    string `yaml:"format"`
	Level     string `yaml:"level"`
	AccessLog bool   `yaml:"access_log"`
}

// HTTP2Config mirrors http.Server.HTTP2; it is an HTTP/2-path section, inert
// on http1.1.
type HTTP2Config struct {
	MaxConcurrentStreams int `yaml:"max_concurrent_streams"`
}

type Server struct {
	HttpVersion           string        `yaml:"http_version"`
	CertFile              string        `yaml:"cert_file"`
	KeyFile               string        `yaml:"key_file"`
	TLSMinVersion         string        `yaml:"tls_min_version"`
	MaxIdleWorkerDuration time.Duration `yaml:"max_idle_worker_duration"`
	TCPKeepalivePeriod    time.Duration `yaml:"tcp_keepalive_period"`
	Concurrency           int           `yaml:"concurrency"`
	ReadTimeout           time.Duration `yaml:"read_timeout"`
	WriteTimeout          time.Duration `yaml:"write_timeout"`
	IdleTimeout           time.Duration `yaml:"idle_timeout"`
	ProxyTimeout          time.Duration `yaml:"proxy_timeout"`
	MaxRequestBodySize    int           `yaml:"max_request_body_size"`
	// HTTP/1.1-path knobs: the fasthttp server's per-connection buffers,
	// which bound request header size (the HTTP/2 stack manages its own
	// framing). Unset or non-positive means the default.
	ReadBufferSize  int `yaml:"read_buffer_size"`
	WriteBufferSize int `yaml:"write_buffer_size"`
	// Unset or non-positive means unlimited (the library default).
	MaxConnsPerIP           int           `yaml:"max_conns_per_ip"`
	MaxRequestsPerConn      int           `yaml:"max_requests_per_conn"`
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout"`
	DNSCacheDuration        time.Duration `yaml:"dns_cache_duration"`
	DisableKeepalive        bool          `yaml:"disable_keepalive"`
	HTTP2                   HTTP2Config   `yaml:"http2"`
	// Opt-in for 1.0 (ADR 0007); see CONTEXT.md, Streamed response.
	Streaming         bool          `yaml:"streaming"`
	StreamIdleTimeout time.Duration `yaml:"stream_idle_timeout"`
}

// TLSMinVersionValue maps the validated tls_min_version onto crypto/tls
// constants; zero keeps the runtime default.
func (s *Server) TLSMinVersionValue() uint16 {
	switch s.TLSMinVersion {
	case TLSMinVersion12:
		return tls.VersionTLS12
	case TLSMinVersion13:
		return tls.VersionTLS13
	}
	return 0
}

type Config struct {
	CustomHeaders        map[string]string `yaml:"custom_headers"`
	HealthCheckerFunc    types.IsHostAlive `yaml:"-"`
	HashFunc             types.HashFunc    `yaml:"-"`
	Monitoring           Monitoring        `yaml:"monitoring"`
	Type                 string            `yaml:"type"`
	Host                 string            `yaml:"host"`
	Port                 string            `yaml:"port"`
	Backends             []Backend         `yaml:"backends"`
	Server               Server            `yaml:"server"`
	Middlewares          []Middleware      `yaml:"middlewares"`
	Logging              Logging           `yaml:"logging"`
	HealthCheckerTime    time.Duration     `yaml:"health_checker_time"`
	HealthCheckerTimeout time.Duration     `yaml:"health_checker_timeout"`
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

	config, err := ParseConfig(configFile)
	if err != nil && !errors.Is(err, ErrConfigFileEmpty) {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return config, err
}

func ParseConfig(configYAML []byte) (*Config, error) {
	// KnownFields: a misspelled or removed key is a startup error, never a
	// silently ignored setting.
	decoder := yaml.NewDecoder(bytes.NewReader(configYAML))
	decoder.KnownFields(true)

	var config Config
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrConfigFileEmpty
		}
		return nil, err
	}

	return &config, nil
}

func (c *Config) PrepareConfig() error {
	if len(c.Backends) == 0 {
		return ErrAtLeastOneBackend
	}

	if c.Host == "" {
		c.Host = "0.0.0.0"
	}

	if c.Port == "" {
		return ErrInvalidPort
	}

	if c.Type == "" {
		c.Type = RoundRobinType
	}

	if !helper.Contains(ValidTypes, c.Type) {
		return fmt.Errorf("Please choose valid load balancing type e.g %v", ValidTypes)
	}

	if c.Type == WeightedRoundRobinType && len(c.Backends) == 1 {
		c.Type = RoundRobinType
	}

	if c.HealthCheckerTime <= 0 {
		c.HealthCheckerTime = DefaultHealthCheckerTime
	}

	if c.HealthCheckerTimeout < 0 {
		return fmt.Errorf("%w, got %v", ErrInvalidHealthCheckerTimeout, c.HealthCheckerTimeout)
	}
	if c.HealthCheckerTimeout == 0 {
		c.HealthCheckerTimeout = DefaultHealthCheckerTimeout
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

	if err := c.Logging.prepareLogging(); err != nil {
		return err
	}

	if err := c.validateMiddlewares(); err != nil {
		return err
	}

	err := c.Server.prepareServer()
	if err != nil {
		return err
	}

	// Default funcs
	// TODO make more flexible
	c.HashFunc = helper.HashFunc
	c.HealthCheckerFunc = http.NewHttpClient(c.Server.DNSCacheDuration, c.HealthCheckerTimeout).IsHostAlive

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

		if c.Type == WeightedRoundRobinType && b.Weight <= 0 {
			return ErrInvalidWeight
		}

		if b.HealthCheckPath == "" {
			b.HealthCheckPath = "/"
		}

		if b.HealthCheckStatus != types.AnySuccessOrRedirect && !isFinalHTTPStatus(int(b.HealthCheckStatus)) {
			return fmt.Errorf("%w: backend at index %d, got %d", ErrInvalidHealthCheckStatus, i, b.HealthCheckStatus)
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
		b.DNSCacheDuration = c.Server.DNSCacheDuration
		b.Streaming = c.Server.Streaming
		b.StreamIdleTimeout = c.Server.StreamIdleTimeout
	}

	return nil
}

func isFinalHTTPStatus(status int) bool {
	return status >= fasthttp.StatusOK && status <= 599
}

// normalizeBackendAddress derives the dialable Backend address (host:port)
// from the config url: an optional http:// scheme and a bare trailing slash
// are stripped, a missing port defaults to 80. Anything divisor cannot honor
// (path, query, userinfo) is rejected, as is https://
// (development/adr/0004-plaintext-only-backends.md).
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

func (l *Logging) prepareLogging() error {
	if l.Format == "" {
		l.Format = DefaultLoggingFormat
	}
	if l.Format != LoggingFormatJSON && l.Format != LoggingFormatConsole {
		return fmt.Errorf("%w, got %q", ErrInvalidLoggingFormat, l.Format)
	}

	if l.Level == "" {
		l.Level = DefaultLoggingLevel
	}
	if _, err := zapcore.ParseLevel(l.Level); err != nil {
		return fmt.Errorf("%w, got %q", ErrInvalidLoggingLevel, l.Level)
	}

	return nil
}

func (s *Server) prepareServer() error {
	// "http1" is the documented spelling; Http1 ("http1.1") is tolerated.
	switch s.HttpVersion {
	case "", "http1", Http1:
		s.HttpVersion = Http1
	case Http2:
	default:
		return fmt.Errorf("%w, got %q", ErrInvalidHttpVersion, s.HttpVersion)
	}

	if s.HttpVersion == Http2 && (s.CertFile == "" || s.KeyFile == "") {
		return ErrHttp2WithoutTls
	}

	if err := s.prepareTLS(); err != nil {
		return err
	}

	return s.applyServerDefaults()
}

func (s *Server) prepareTLS() error {
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

	if s.TLSMinVersion != "" && s.TLSMinVersionValue() == 0 {
		return fmt.Errorf("%w, got %q", ErrInvalidTLSMinVersion, s.TLSMinVersion)
	}

	return nil
}

// applyServerDefaults fills every unset or non-positive server knob with its
// default; the duration knobs reject negatives instead of defaulting.
func (s *Server) applyServerDefaults() error {
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

	if s.StreamIdleTimeout < 0 {
		return fmt.Errorf("%w, got %v", ErrInvalidStreamIdleTimeout, s.StreamIdleTimeout)
	}
	if s.StreamIdleTimeout == 0 {
		s.StreamIdleTimeout = DefaultStreamIdleTimeout
	}

	if s.MaxRequestBodySize <= 0 {
		s.MaxRequestBodySize = DefaultMaxRequestBodySize
	}

	if s.GracefulShutdownTimeout < 0 {
		return fmt.Errorf("%w, got %v", ErrInvalidGracefulShutdownTimeout, s.GracefulShutdownTimeout)
	}
	if s.GracefulShutdownTimeout == 0 {
		s.GracefulShutdownTimeout = DefaultGracefulShutdownTimeout
	}

	if s.ReadBufferSize <= 0 {
		s.ReadBufferSize = DefaultReadBufferSize
	}
	if s.WriteBufferSize <= 0 {
		s.WriteBufferSize = DefaultWriteBufferSize
	}

	if s.MaxConnsPerIP <= 0 {
		s.MaxConnsPerIP = DefaultMaxConnsPerIP
	}
	if s.MaxRequestsPerConn <= 0 {
		s.MaxRequestsPerConn = DefaultMaxRequestsPerConn
	}

	if s.DNSCacheDuration < 0 {
		return fmt.Errorf("%w, got %v", ErrInvalidDNSCacheDuration, s.DNSCacheDuration)
	}
	if s.DNSCacheDuration == 0 {
		s.DNSCacheDuration = DefaultDNSCacheDuration
	}

	// net/http silently replaces a value above math.MaxInt32 with its default.
	if s.HTTP2.MaxConcurrentStreams > math.MaxInt32 {
		return fmt.Errorf("%w, got %d", ErrInvalidHTTP2MaxConcurrentStreams, s.HTTP2.MaxConcurrentStreams)
	}
	if s.HTTP2.MaxConcurrentStreams <= 0 {
		s.HTTP2.MaxConcurrentStreams = DefaultHTTP2MaxConcurrentStreams
	}

	return nil
}

// validateMiddlewares is the one place a Middleware entry is validated; the
// executor trusts the config it is handed.
func (c *Config) validateMiddlewares() error {
	for i, mw := range c.Middlewares {
		if mw.Name == "" {
			return fmt.Errorf("%w: middleware at index %d", ErrMiddlewareNameRequired, i)
		}
		if mw.Disabled {
			continue
		}
		if mw.Code == "" && mw.File == "" {
			return fmt.Errorf("%w: middleware %q", ErrMiddlewareCodeAndFileEmpty, mw.Name)
		}
		if mw.Code != "" && mw.File != "" {
			return fmt.Errorf("%w: middleware %q", ErrMiddlewareCodeAndFileBothSet, mw.Name)
		}
	}
	return nil
}
