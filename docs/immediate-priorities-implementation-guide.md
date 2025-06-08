# Immediate Priorities Implementation Guide

## 🚨 Critical Issues to Address First

This document provides detailed implementation guidance for the most urgent improvements needed for Divisor to be production-ready and competitive.

## Priority 1: HTTPS Backend Support (CRITICAL)

### Current Problem
```yaml
# Current limitation - only HTTP backends supported
backends:
  - url: "http://backend1.example.com"  # ❌ Security risk in production
    health_check_path: "/health"
```

### Required Implementation

#### 1.1 Backend TLS Configuration
```go
// Add TLS support to backend configuration
type BackendConfig struct {
    URL                     string        `yaml:"url"`
    HealthCheckPath         string        `yaml:"health_check_path"`
    // New TLS fields
    TLSEnabled              bool          `yaml:"tls_enabled"`
    TLSSkipVerify          bool          `yaml:"tls_skip_verify"`
    TLSCACert              string        `yaml:"tls_ca_cert"`
    TLSClientCert          string        `yaml:"tls_client_cert"`
    TLSClientKey           string        `yaml:"tls_client_key"`
    TLSMinVersion          string        `yaml:"tls_min_version"`
    TLSMaxVersion          string        `yaml:"tls_max_version"`
    TLSCipherSuites        []string      `yaml:"tls_cipher_suites"`
}
```

#### 1.2 TLS Client Implementation
```go
package backend

import (
    "crypto/tls"
    "crypto/x509"
    "fmt"
    "io/ioutil"
)

type TLSConfig struct {
    CACert     []byte
    ClientCert []byte
    ClientKey  []byte
    SkipVerify bool
    MinVersion uint16
    MaxVersion uint16
}

func NewTLSConfig(config BackendConfig) (*tls.Config, error) {
    tlsConfig := &tls.Config{
        InsecureSkipVerify: config.TLSSkipVerify,
        MinVersion:         getTLSVersion(config.TLSMinVersion),
        MaxVersion:         getTLSVersion(config.TLSMaxVersion),
    }

    // Load CA certificate if provided
    if config.TLSCACert != "" {
        caCert, err := ioutil.ReadFile(config.TLSCACert)
        if err != nil {
            return nil, fmt.Errorf("failed to read CA cert: %v", err)
        }
        
        caCertPool := x509.NewCertPool()
        if !caCertPool.AppendCertsFromPEM(caCert) {
            return nil, fmt.Errorf("failed to parse CA cert")
        }
        tlsConfig.RootCAs = caCertPool
    }

    // Load client certificate if provided
    if config.TLSClientCert != "" && config.TLSClientKey != "" {
        cert, err := tls.LoadX509KeyPair(config.TLSClientCert, config.TLSClientKey)
        if err != nil {
            return nil, fmt.Errorf("failed to load client cert: %v", err)
        }
        tlsConfig.Certificates = []tls.Certificate{cert}
    }

    return tlsConfig, nil
}

func getTLSVersion(version string) uint16 {
    switch version {
    case "1.0":
        return tls.VersionTLS10
    case "1.1":
        return tls.VersionTLS11
    case "1.2":
        return tls.VersionTLS12
    case "1.3":
        return tls.VersionTLS13
    default:
        return tls.VersionTLS12 // Default to TLS 1.2
    }
}
```

#### 1.3 Updated Backend Client
```go
package backend

import (
    "crypto/tls"
    "github.com/valyala/fasthttp"
    "time"
)

type HTTPSBackend struct {
    URL       string
    TLSConfig *tls.Config
    Client    *fasthttp.HostClient
}

func NewHTTPSBackend(config BackendConfig) (*HTTPSBackend, error) {
    tlsConfig, err := NewTLSConfig(config)
    if err != nil {
        return nil, err
    }

    client := &fasthttp.HostClient{
        Addr:                config.URL,
        TLSConfig:          tlsConfig,
        MaxConns:           config.MaxConn,
        MaxConnWaitTimeout: config.MaxConnTimeout,
        MaxConnDuration:    config.MaxConnDuration,
        MaxIdleConnDuration: config.MaxIdleConnDuration,
        MaxIdemponentCallAttempts: config.MaxIdemponentCallAttempts,
    }

    return &HTTPSBackend{
        URL:       config.URL,
        TLSConfig: tlsConfig,
        Client:    client,
    }, nil
}

func (hb *HTTPSBackend) Forward(req *fasthttp.Request, resp *fasthttp.Response) error {
    return hb.Client.Do(req, resp)
}
```

#### 1.4 Updated Configuration Example
```yaml
# config.yaml with HTTPS backend support
backends:
  - url: "https://backend1.example.com"
    health_check_path: "/health"
    tls_enabled: true
    tls_skip_verify: false
    tls_ca_cert: "/path/to/ca.crt"
    tls_client_cert: "/path/to/client.crt"
    tls_client_key: "/path/to/client.key"
    tls_min_version: "1.2"
    max_conn: 512
    max_conn_timeout: 30s
    max_conn_duration: 10s
    max_idle_conn_duration: 10s
    max_idemponent_call_attempts: 5
    
  - url: "https://backend2.example.com"
    health_check_path: "/health"
    tls_enabled: true
    tls_skip_verify: true  # For development/testing only
```

### Implementation Timeline: Week 1

- **Day 1**: Implement TLS configuration structures
- **Day 2**: Create TLS client implementation
- **Day 3**: Update backend creation logic
- **Day 4**: Add configuration validation
- **Day 5**: Write unit tests
- **Day 6**: Integration testing
- **Day 7**: Documentation update

## Priority 2: Hot Configuration Reload

### Current Problem
- Configuration changes require restart
- Downtime during configuration updates
- No graceful configuration validation

### Required Implementation

#### 2.1 Configuration Watcher
```go
package config

import (
    "context"
    "fmt"
    "log"
    "os"
    "path/filepath"
    "time"
    
    "github.com/fsnotify/fsnotify"
    "gopkg.in/yaml.v3"
)

type ConfigWatcher struct {
    configPath   string
    watcher      *fsnotify.Watcher
    reloadChan   chan Config
    errorChan    chan error
    ctx          context.Context
    cancel       context.CancelFunc
}

func NewConfigWatcher(configPath string) (*ConfigWatcher, error) {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    ctx, cancel := context.WithCancel(context.Background())

    cw := &ConfigWatcher{
        configPath: configPath,
        watcher:    watcher,
        reloadChan: make(chan Config, 1),
        errorChan:  make(chan error, 1),
        ctx:        ctx,
        cancel:     cancel,
    }

    // Watch the config file directory
    configDir := filepath.Dir(configPath)
    err = watcher.Add(configDir)
    if err != nil {
        return nil, err
    }

    go cw.watch()
    return cw, nil
}

func (cw *ConfigWatcher) watch() {
    defer cw.watcher.Close()

    for {
        select {
        case <-cw.ctx.Done():
            return
        case event, ok := <-cw.watcher.Events:
            if !ok {
                return
            }

            // Check if it's our config file and it was written to
            if event.Name == cw.configPath && event.Op&fsnotify.Write == fsnotify.Write {
                log.Printf("Config file changed, reloading...")
                
                // Small delay to ensure file write is complete
                time.Sleep(100 * time.Millisecond)
                
                newConfig, err := cw.loadAndValidateConfig()
                if err != nil {
                    cw.errorChan <- fmt.Errorf("failed to reload config: %v", err)
                    continue
                }

                select {
                case cw.reloadChan <- newConfig:
                    log.Printf("Config reloaded successfully")
                case <-cw.ctx.Done():
                    return
                }
            }
        case err, ok := <-cw.watcher.Errors:
            if !ok {
                return
            }
            cw.errorChan <- err
        }
    }
}

func (cw *ConfigWatcher) loadAndValidateConfig() (Config, error) {
    var config Config

    data, err := os.ReadFile(cw.configPath)
    if err != nil {
        return config, err
    }

    err = yaml.Unmarshal(data, &config)
    if err != nil {
        return config, err
    }

    // Validate configuration
    err = ValidateConfig(&config)
    if err != nil {
        return config, err
    }

    return config, nil
}

func (cw *ConfigWatcher) ConfigReloads() <-chan Config {
    return cw.reloadChan
}

func (cw *ConfigWatcher) Errors() <-chan error {
    return cw.errorChan
}

func (cw *ConfigWatcher) Stop() {
    cw.cancel()
}
```

#### 2.2 Graceful Config Reload in Load Balancer
```go
package loadbalancer

import (
    "context"
    "log"
    "sync"
    "sync/atomic"
    "time"
)

type LoadBalancer struct {
    config        atomic.Value // stores *Config
    backends      atomic.Value // stores []Backend
    configWatcher *config.ConfigWatcher
    mu            sync.RWMutex
    shutdownChan  chan struct{}
}

func (lb *LoadBalancer) StartConfigWatcher(configPath string) error {
    watcher, err := config.NewConfigWatcher(configPath)
    if err != nil {
        return err
    }

    lb.configWatcher = watcher

    go lb.handleConfigReloads()
    return nil
}

func (lb *LoadBalancer) handleConfigReloads() {
    for {
        select {
        case newConfig := <-lb.configWatcher.ConfigReloads():
            if err := lb.reloadConfig(&newConfig); err != nil {
                log.Printf("Failed to apply new config: %v", err)
                continue
            }
            log.Printf("Configuration reloaded successfully")

        case err := <-lb.configWatcher.Errors():
            log.Printf("Config watcher error: %v", err)

        case <-lb.shutdownChan:
            return
        }
    }
}

func (lb *LoadBalancer) reloadConfig(newConfig *Config) error {
    // Create new backends
    newBackends, err := lb.createBackends(newConfig.Backends)
    if err != nil {
        return err
    }

    // Graceful transition
    oldBackends := lb.getBackends()

    // Update configuration atomically
    lb.config.Store(newConfig)
    lb.backends.Store(newBackends)

    // Gracefully shutdown old backends after a delay
    go func() {
        time.Sleep(30 * time.Second) // Allow in-flight requests to complete
        lb.shutdownBackends(oldBackends)
    }()

    return nil
}

func (lb *LoadBalancer) createBackends(configs []BackendConfig) ([]Backend, error) {
    backends := make([]Backend, 0, len(configs))

    for _, config := range configs {
        backend, err := NewBackend(config)
        if err != nil {
            return nil, err
        }
        backends = append(backends, backend)
    }

    return backends, nil
}

func (lb *LoadBalancer) shutdownBackends(backends []Backend) {
    for _, backend := range backends {
        if closer, ok := backend.(interface{ Close() error }); ok {
            closer.Close()
        }
    }
}

func (lb *LoadBalancer) getBackends() []Backend {
    return lb.backends.Load().([]Backend)
}

func (lb *LoadBalancer) getConfig() *Config {
    return lb.config.Load().(*Config)
}
```

#### 2.3 Signal-Based Reload
```go
package main

import (
    "os"
    "os/signal"
    "syscall"
    "log"
)

func (app *Application) setupSignalHandling() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)

    go func() {
        for sig := range sigChan {
            switch sig {
            case syscall.SIGHUP:
                log.Printf("Received SIGHUP, reloading configuration...")
                if err := app.reloadConfig(); err != nil {
                    log.Printf("Failed to reload config: %v", err)
                }
            case syscall.SIGTERM, syscall.SIGINT:
                log.Printf("Received shutdown signal, shutting down gracefully...")
                app.shutdown()
                return
            }
        }
    }()
}

func (app *Application) reloadConfig() error {
    newConfig, err := config.LoadConfig(app.configPath)
    if err != nil {
        return err
    }

    return app.loadBalancer.ReloadConfig(newConfig)
}
```

### Implementation Timeline: Week 2

- **Day 1**: Implement configuration watcher
- **Day 2**: Add atomic configuration swapping
- **Day 3**: Implement graceful backend transitions
- **Day 4**: Add signal handling for reload
- **Day 5**: Write tests for reload scenarios
- **Day 6**: Test with production-like scenarios
- **Day 7**: Documentation and examples

## Priority 3: Comprehensive Testing Suite

### Current Problem
- No visible test coverage information
- Uncertain reliability guarantees
- Potential production issues

### Required Implementation

#### 3.1 Unit Testing Framework
```go
// test/unit/backend_test.go
package backend_test

import (
    "testing"
    "time"
    "crypto/tls"
    "net/http"
    "net/http/httptest"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/your-org/divisor/pkg/backend"
)

func TestHTTPSBackendCreation(t *testing.T) {
    tests := []struct {
        name     string
        config   backend.BackendConfig
        wantErr  bool
        errMsg   string
    }{
        {
            name: "valid HTTPS backend",
            config: backend.BackendConfig{
                URL:           "https://example.com",
                TLSEnabled:    true,
                TLSSkipVerify: true,
            },
            wantErr: false,
        },
        {
            name: "invalid TLS cert path",
            config: backend.BackendConfig{
                URL:           "https://example.com",
                TLSEnabled:    true,
                TLSCACert:     "/nonexistent/path",
            },
            wantErr: true,
            errMsg:  "failed to read CA cert",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            backend, err := backend.NewHTTPSBackend(tt.config)
            
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errMsg)
                assert.Nil(t, backend)
            } else {
                require.NoError(t, err)
                assert.NotNil(t, backend)
                assert.Equal(t, tt.config.URL, backend.URL)
            }
        })
    }
}

func TestHTTPSBackendRequest(t *testing.T) {
    // Create test TLS server
    server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("Hello from HTTPS backend"))
    }))
    defer server.Close()

    // Create backend with test server
    config := backend.BackendConfig{
        URL:           server.URL,
        TLSEnabled:    true,
        TLSSkipVerify: true,
    }

    backend, err := backend.NewHTTPSBackend(config)
    require.NoError(t, err)

    // Test request
    req := fasthttp.AcquireRequest()
    resp := fasthttp.AcquireResponse()
    defer func() {
        fasthttp.ReleaseRequest(req)
        fasthttp.ReleaseResponse(resp)
    }()

    req.SetRequestURI(server.URL + "/test")
    req.Header.SetMethod("GET")

    err = backend.Forward(req, resp)
    require.NoError(t, err)
    assert.Equal(t, 200, resp.StatusCode())
    assert.Equal(t, "Hello from HTTPS backend", string(resp.Body()))
}
```

#### 3.2 Integration Testing
```go
// test/integration/loadbalancer_test.go
package integration_test

import (
    "testing"
    "time"
    "net/http"
    "net/http/httptest"
    "sync"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/your-org/divisor/pkg/loadbalancer"
    "github.com/your-org/divisor/pkg/config"
)

func TestLoadBalancerHTTPSBackends(t *testing.T) {
    // Create multiple test backends
    backend1 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("backend1"))
    }))
    defer backend1.Close()

    backend2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("backend2"))
    }))
    defer backend2.Close()

    // Configure load balancer
    cfg := &config.Config{
        Type: "round-robin",
        Port: 0, // Use random port for testing
        Backends: []config.BackendConfig{
            {
                URL:           backend1.URL,
                TLSEnabled:    true,
                TLSSkipVerify: true,
            },
            {
                URL:           backend2.URL,
                TLSEnabled:    true,
                TLSSkipVerify: true,
            },
        },
    }

    lb, err := loadbalancer.New(cfg)
    require.NoError(t, err)

    // Start load balancer
    go func() {
        err := lb.Start()
        require.NoError(t, err)
    }()

    // Wait for startup
    time.Sleep(100 * time.Millisecond)

    // Test round-robin distribution
    responses := make(map[string]int)
    for i := 0; i < 10; i++ {
        resp, err := http.Get("http://localhost:" + lb.GetPort())
        require.NoError(t, err)
        
        body := readBody(resp.Body)
        responses[body]++
        resp.Body.Close()
    }

    // Should distribute evenly
    assert.Equal(t, 5, responses["backend1"])
    assert.Equal(t, 5, responses["backend2"])

    lb.Shutdown()
}

func TestConfigurationReload(t *testing.T) {
    // Create temporary config file
    configFile := createTempConfig(t)
    defer os.Remove(configFile)

    // Start load balancer with initial config
    lb, err := loadbalancer.NewFromFile(configFile)
    require.NoError(t, err)

    err = lb.StartConfigWatcher(configFile)
    require.NoError(t, err)

    go func() {
        err := lb.Start()
        require.NoError(t, err)
    }()

    time.Sleep(100 * time.Millisecond)

    // Verify initial backend count
    assert.Equal(t, 2, lb.BackendCount())

    // Update configuration
    updateConfig(t, configFile, 3) // Add third backend

    // Wait for reload
    time.Sleep(500 * time.Millisecond)

    // Verify backend count updated
    assert.Equal(t, 3, lb.BackendCount())

    lb.Shutdown()
}
```

#### 3.3 Performance Benchmarks
```go
// test/benchmark/loadbalancer_benchmark_test.go
package benchmark_test

import (
    "testing"
    "net/http"
    "net/http/httptest"
    "sync"
    
    "github.com/valyala/fasthttp"
    "github.com/your-org/divisor/pkg/loadbalancer"
)

func BenchmarkLoadBalancer(b *testing.B) {
    // Setup test backend
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    }))
    defer backend.Close()

    // Setup load balancer
    cfg := &config.Config{
        Type: "round-robin",
        Backends: []config.BackendConfig{
            {URL: backend.URL},
        },
    }

    lb, err := loadbalancer.New(cfg)
    if err != nil {
        b.Fatal(err)
    }

    go func() {
        lb.Start()
    }()

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            req := fasthttp.AcquireRequest()
            resp := fasthttp.AcquireResponse()
            
            req.SetRequestURI("http://localhost:" + lb.GetPort() + "/")
            req.Header.SetMethod("GET")

            client := &fasthttp.Client{}
            err := client.Do(req, resp)
            if err != nil {
                b.Error(err)
            }

            fasthttp.ReleaseRequest(req)
            fasthttp.ReleaseResponse(resp)
        }
    })
}

func BenchmarkTLSBackend(b *testing.B) {
    // Setup TLS backend
    backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    }))
    defer backend.Close()

    // Create HTTPS backend
    backendConfig := backend.BackendConfig{
        URL:           backend.URL,
        TLSEnabled:    true,
        TLSSkipVerify: true,
    }

    httpsBackend, err := backend.NewHTTPSBackend(backendConfig)
    if err != nil {
        b.Fatal(err)
    }

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            req := fasthttp.AcquireRequest()
            resp := fasthttp.AcquireResponse()
            
            req.SetRequestURI(backend.URL + "/")
            req.Header.SetMethod("GET")

            err := httpsBackend.Forward(req, resp)
            if err != nil {
                b.Error(err)
            }

            fasthttp.ReleaseRequest(req)
            fasthttp.ReleaseResponse(resp)
        }
    })
}
```

#### 3.4 Test Coverage Configuration
```yaml
# .github/workflows/test.yml
name: Test and Coverage

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v3
      with:
        go-version: 1.21
    
    - name: Install dependencies
      run: go mod download
    
    - name: Run tests with coverage
      run: |
        go test -v -race -coverprofile=coverage.out ./...
        go tool cover -html=coverage.out -o coverage.html
    
    - name: Upload coverage to Codecov
      uses: codecov/codecov-action@v3
      with:
        file: ./coverage.out
    
    - name: Check coverage threshold
      run: |
        COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
        echo "Coverage: $COVERAGE%"
        if (( $(echo "$COVERAGE < 80" | bc -l) )); then
          echo "Coverage is below 80%"
          exit 1
        fi
```

### Implementation Timeline: Week 3

- **Day 1**: Set up testing framework and basic unit tests
- **Day 2**: Write comprehensive backend tests
- **Day 3**: Create integration test suite
- **Day 4**: Implement performance benchmarks
- **Day 5**: Set up CI/CD with coverage reporting
- **Day 6**: Add edge case and error condition tests
- **Day 7**: Documentation for testing practices

## Priority 4: Security Documentation and Implementation

### Current Problem
- No security considerations documented
- Missing threat model
- No security best practices

### Required Implementation

#### 4.1 Security Configuration
```go
// pkg/security/security.go
package security

import (
    "crypto/tls"
    "time"
)

type SecurityConfig struct {
    TLS                 TLSConfig           `yaml:"tls"`
    RateLimit          RateLimitConfig     `yaml:"rate_limit"`
    Headers            SecurityHeaders     `yaml:"headers"`
    Authentication     AuthConfig          `yaml:"authentication"`
    Logging            SecurityLogging     `yaml:"logging"`
}

type TLSConfig struct {
    MinVersion           string   `yaml:"min_version"`
    MaxVersion           string   `yaml:"max_version"`
    CipherSuites         []string `yaml:"cipher_suites"`
    PreferServerCiphers  bool     `yaml:"prefer_server_ciphers"`
    HSTSMaxAge           int      `yaml:"hsts_max_age"`
    HSTSIncludeSubdomain bool     `yaml:"hsts_include_subdomain"`
}

type RateLimitConfig struct {
    Enabled          bool          `yaml:"enabled"`
    RequestsPerSecond int           `yaml:"requests_per_second"`
    BurstSize        int           `yaml:"burst_size"`
    WindowSize       time.Duration `yaml:"window_size"`
    BlockDuration    time.Duration `yaml:"block_duration"`
}

type SecurityHeaders struct {
    XFrameOptions        string `yaml:"x_frame_options"`
    XContentTypeOptions  string `yaml:"x_content_type_options"`
    XSSProtection        string `yaml:"xss_protection"`
    ContentSecurityPolicy string `yaml:"content_security_policy"`
    ReferrerPolicy       string `yaml:"referrer_policy"`
}
```

#### 4.2 Security Middleware
```go
// pkg/security/middleware.go
package security

import (
    "fmt"
    "time"
    "sync"
    "golang.org/x/time/rate"
    "github.com/valyala/fasthttp"
)

type SecurityMiddleware struct {
    rateLimiter *RateLimiter
    headers     SecurityHeaders
    config      SecurityConfig
}

type RateLimiter struct {
    limiters map[string]*rate.Limiter
    mu       sync.RWMutex
    config   RateLimitConfig
}

func NewSecurityMiddleware(config SecurityConfig) *SecurityMiddleware {
    return &SecurityMiddleware{
        rateLimiter: NewRateLimiter(config.RateLimit),
        headers:     config.Headers,
        config:      config,
    }
}

func (sm *SecurityMiddleware) Handle(ctx *fasthttp.RequestCtx, next fasthttp.RequestHandler) {
    // Rate limiting
    if sm.config.RateLimit.Enabled {
        clientIP := string(ctx.Request.Header.Peek("X-Real-IP"))
        if clientIP == "" {
            clientIP = ctx.RemoteIP().String()
        }

        if !sm.rateLimiter.Allow(clientIP) {
            ctx.SetStatusCode(fasthttp.StatusTooManyRequests)
            ctx.SetBodyString("Rate limit exceeded")
            return
        }
    }

    // Set security headers
    sm.setSecurityHeaders(ctx)

    // Call next handler
    next(ctx)
}

func (sm *SecurityMiddleware) setSecurityHeaders(ctx *fasthttp.RequestCtx) {
    headers := &ctx.Response.Header

    if sm.headers.XFrameOptions != "" {
        headers.Set("X-Frame-Options", sm.headers.XFrameOptions)
    }

    if sm.headers.XContentTypeOptions != "" {
        headers.Set("X-Content-Type-Options", sm.headers.XContentTypeOptions)
    }

    if sm.headers.XSSProtection != "" {
        headers.Set("X-XSS-Protection", sm.headers.XSSProtection)
    }

    if sm.headers.ContentSecurityPolicy != "" {
        headers.Set("Content-Security-Policy", sm.headers.ContentSecurityPolicy)
    }

    if sm.headers.ReferrerPolicy != "" {
        headers.Set("Referrer-Policy", sm.headers.ReferrerPolicy)
    }

    // HSTS header for HTTPS
    if ctx.IsTLS() && sm.config.TLS.HSTSMaxAge > 0 {
        hsts := fmt.Sprintf("max-age=%d", sm.config.TLS.HSTSMaxAge)
        if sm.config.TLS.HSTSIncludeSubdomain {
            hsts += "; includeSubDomains"
        }
        headers.Set("Strict-Transport-Security", hsts)
    }
}

func NewRateLimiter(config RateLimitConfig) *RateLimiter {
    return &RateLimiter{
        limiters: make(map[string]*rate.Limiter),
        config:   config,
    }
}

func (rl *RateLimiter) Allow(clientIP string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    limiter, exists := rl.limiters[clientIP]
    if !exists {
        limiter = rate.NewLimiter(rate.Limit(rl.config.RequestsPerSecond), rl.config.BurstSize)
        rl.limiters[clientIP] = limiter
    }

    return limiter.Allow()
}
```

#### 4.3 Security Documentation
```markdown
# Security Guide

## Threat Model

### Assets
- Load balancer configuration
- Backend server connections
- Request/response data
- Monitoring metrics
- TLS certificates and keys

### Threats
1. **Man-in-the-Middle Attacks**
   - Risk: Interception of traffic between client and load balancer
   - Mitigation: TLS encryption, certificate validation

2. **DDoS Attacks**
   - Risk: Service unavailability due to traffic flooding
   - Mitigation: Rate limiting, traffic shaping

3. **Configuration Tampering**
   - Risk: Unauthorized modification of load balancer settings
   - Mitigation: File permissions, configuration validation

4. **Backend Impersonation**
   - Risk: Malicious servers masquerading as legitimate backends
   - Mitigation: mTLS, certificate pinning

### Security Configuration

#### TLS Best Practices
```yaml
server:
  tls_min_version: "1.2"
  tls_cipher_suites:
    - "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"
    - "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
    - "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305"
  prefer_server_ciphers: true

security:
  rate_limit:
    enabled: true
    requests_per_second: 100
    burst_size: 200
    window_size: 1m
    block_duration: 5m
  
  headers:
    x_frame_options: "DENY"
    x_content_type_options: "nosniff"
    xss_protection: "1; mode=block"
    content_security_policy: "default-src 'self'"
    referrer_policy: "strict-origin-when-cross-origin"
```

#### Backend Security
```yaml
backends:
  - url: "https://backend.example.com"
    tls_enabled: true
    tls_skip_verify: false
    tls_ca_cert: "/path/to/ca.crt"
    tls_client_cert: "/path/to/client.crt"
    tls_client_key: "/path/to/client.key"
    tls_min_version: "1.2"
```

## Deployment Security

### File Permissions
```bash
# Configuration file
chmod 600 config.yaml
chown divisor:divisor config.yaml

# TLS certificates
chmod 600 /path/to/certs/*.key
chmod 644 /path/to/certs/*.crt
chown divisor:divisor /path/to/certs/*
```

### Network Security
- Run on non-privileged ports (>1024)
- Use firewall rules to restrict access
- Enable connection limits
- Monitor for suspicious traffic patterns

### Logging and Monitoring
- Log all configuration changes
- Monitor for rate limit violations
- Alert on TLS certificate expiration
- Track backend health status changes
```

### Implementation Timeline: Week 4

- **Day 1**: Implement security configuration structures
- **Day 2**: Create security middleware for rate limiting and headers
- **Day 3**: Add TLS configuration validation
- **Day 4**: Write security documentation and threat model
- **Day 5**: Create security test cases
- **Day 6**: Security audit and penetration testing
- **Day 7**: Deployment security guidelines

## Summary and Next Steps

### Week 1-4 Deliverables
- ✅ HTTPS backend support with full TLS configuration
- ✅ Hot configuration reload without downtime
- ✅ Comprehensive testing suite with >80% coverage
- ✅ Security implementation and documentation

### Success Metrics
- **Security**: All backends use HTTPS in production configurations
- **Reliability**: Zero-downtime configuration changes
- **Quality**: >90% test coverage with automated CI/CD
- **Documentation**: Complete security guide with threat model

### Post-Implementation Benefits
1. **Production Ready**: Safe for enterprise production deployments
2. **Operational Excellence**: Easy configuration management
3. **Security Compliant**: Meets modern security standards
4. **Developer Confidence**: Comprehensive test coverage

This implementation guide provides the foundation for making Divisor a reliable, secure, and production-ready load balancer that can compete with established solutions. 