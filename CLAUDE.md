# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Build
```bash
go build -o divisor
```

### Run
```bash
# Default config (config.yaml in current directory)
./divisor

# With specific config file (use absolute path)
./divisor --config /absolute/path/to/config.yaml
```

### Testing
```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for specific package
go test ./core/round-robin
go test ./internal/proxy

# Run single test
go test -run TestFunctionName ./package/path

# Run the black-box integration suite (requires a running Docker daemon;
# opt-in via env var; its own Go module, so plain `go test ./...` never
# touches it)
DIVISOR_INTEGRATION=1 go -C integration-test test -v -timeout 20m .
```

### Install
```bash
go install github.com/aaydin-tr/divisor@latest
```

## Architecture

### Core Load Balancing System

The load balancer uses a **factory pattern** via `balancer.NewBalancer()` which selects the appropriate algorithm implementation based on `config.Type`. All algorithms implement the `types.IBalancer` interface.

**Algorithm Implementations** (in `core/`):
- `round-robin` - Sequential server rotation
- `w-round-robin` - Weighted rotation (requires `weight` in backend config)
- `ip-hash` - Consistent hashing based on client IP (uses `pkg/consistent`)
- `random` - Random server selection
- `least-connection` - Routes to server with fewest active connections
- `least-response-time` - Routes to server with lowest average response time

All algorithms share common behavior:
- Health checking via goroutine that runs every `config.HealthCheckerTime`
- Graceful shutdown support via `Shutdown()` method
- Statistics tracking via `Stats()` method returning `[]types.ProxyStat`

### Proxy Layer

`internal/proxy` handles the actual HTTP proxying:
- `ProxyClient` wraps `fasthttp.HostClient` for each backend
- Removes hop-by-hop headers (Connection, Keep-Alive, etc.)
- Sets `X-Forwarded-For` header with client IP
- Supports custom headers with special variables: `$remote_addr`, `$time`, `$uuid`, `$incremental`
- Tracks metrics: total requests, average response time, last use time, connection count

### Configuration System

`pkg/config`:
- YAML-based configuration with validation in `PrepareConfig()`
- Auto-sets defaults if not specified (e.g., `health_checker_time: 30s`, `type: round-robin`)
- Backend URLs have protocols stripped (http:// or https://)
- HTTP/2 requires TLS (cert_file + key_file must be provided)
- Weighted round-robin with single backend auto-converts to regular round-robin

### Monitoring

`internal/monitoring`:
- Separate HTTP server for metrics (default: localhost:8001)
- Provides real-time stats: CPU, RAM, goroutines, open connections
- Prometheus metrics endpoint at `/metrics`
- Per-backend stats: average response time, request count, last use time

### Main Server Flow

1. Parse config file → `config.ParseConfigFile()`
2. Prepare/validate config → `config.PrepareConfig()`
3. Create balancer with algorithm → `balancer.NewBalancer()`
4. Start health checkers (goroutines for each algorithm)
5. Start monitoring server (goroutine)
6. Start main fasthttp server
7. Listen for SIGINT/SIGTERM for graceful shutdown (30s timeout)

### Graceful Shutdown

Implemented in `performGracefulShutdown()`:
- Stops accepting new connections
- Waits for in-flight requests to complete
- Stops health checker goroutines
- Closes idle connections via `balancer.Shutdown()`
- 30-second timeout enforced

## Key Implementation Details

### Algorithm Selection
All algorithms registered in `core/balancer.go` map:
```go
var balancers = map[string]func(...) types.IBalancer{
    "round-robin": round_robin.NewRoundRobin,
    "w-round-robin": w_round_robin.NewWRoundRobin,
    // ...
}
```

### Backend Health Checking
Each algorithm maintains `stopHealthChecker` channel and runs periodic health checks via ticker. Failed backends are marked but not removed, allowing recovery.

### Consistent Hashing (IP-Hash)
Uses `pkg/consistent` package implementing consistent hashing ring for stable IP-to-backend mapping.

### HTTP/2 Support
When `server.http_version: http2`, divisor serves via `net/http` + `golang.org/x/net/http2` instead of fasthttp; `internal/proxy/nethttp_adapter.go` bridges each request into a synthetic `fasthttp.RequestCtx` so the balancer/proxy path stays shared. TLS is mandatory on this path (no h2c). Backends always speak HTTP/1.1 over plain HTTP.

## Testing Notes

- Tests use mock implementations in `mocks/mocks.go`
- Each algorithm has `*_test.go` with unit tests
- Config validation tested in `pkg/config/config_test.go`
- Proxy behavior tested in `internal/proxy/proxy_test.go`
- `integration-test/` is a black-box Docker suite (dockertest): divisor and
  purpose-built echo backends run as containers, driven over real HTTP/1.1,
  HTTP/2, and TLS (see `docs/adr/0001-black-box-integration-suite.md` and
  `CONTEXT.md` for its vocabulary). Gated by `DIVISOR_INTEGRATION=1`; runs in
  CI via `.github/workflows/integration.yml`. Some tests intentionally assert
  the agreed 1.0 spec and stay red until the behavior ships: startup-down
  backends rejoining, 502 (not 500) for unreachable backends, staying up when
  all backends are down, bounded failure for hanging backends, 413 for
  oversized bodies.
