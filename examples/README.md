# Divisor Configuration Examples

This directory contains example configurations for different use cases and deployment scenarios.

## Quick Start

**Minimal setup** - Just getting started:
- [`basic.config.yaml`](basic.config.yaml) - Simplest possible configuration (2 lines!)

## By Load Balancing Algorithm

**Round Robin** (default) - Distribute requests evenly:
- [`basic.config.yaml`](basic.config.yaml) - Simple round-robin setup

**Weighted Round Robin** - Distribute based on backend capacity:
- [`w-round-robin.config.yaml`](w-round-robin.config.yaml) - Weight-based distribution

**IP Hash** - Session affinity (same client → same backend):
- [`ip-hash.config.yaml`](ip-hash.config.yaml) - Perfect for stateful applications

**Least Connection** - Dynamic balancing to least busy backend:
- [`least-connection.config.yaml`](least-connection.config.yaml) - Optimal for mixed workloads

**Least Response Time** - Route to fastest backend:
- Use `type: least-response-time` in your config

**Random** - Random backend selection:
- Use `type: random` in your config

## By Use Case

**Production Deployment**:
- [`production.config.yaml`](production.config.yaml) - TLS, monitoring, custom headers, optimized timeouts

**HTTP/2 Setup**:
- [`http2-tls.config.yaml`](http2-tls.config.yaml) - HTTP/2 with TLS configuration

**Custom Middleware**:
- [`middleware.config.yaml`](middleware.config.yaml) - Rate limiting, auth, logging examples

**Complete Reference**:
- [`advanced.config.yaml`](advanced.config.yaml) - All available options with detailed comments

## Configuration Guide

### Choosing the Right Algorithm

| Algorithm | Best For | When to Use |
|-----------|----------|-------------|
| `round-robin` | Simple load distribution | Backends have similar capacity, stateless apps |
| `w-round-robin` | Uneven backend capacity | Different server specs (e.g., 2x CPU, 4x CPU) |
| `ip-hash` | Session persistence | Shopping carts, user sessions on backend |
| `least-connection` | Variable request durations | Mixed workloads (fast + slow requests) |
| `least-response-time` | Performance optimization | Backends with varying performance |
| `random` | Simple randomization | Testing, development environments |

### Common Patterns

**Development**:
```yaml
type: round-robin
port: 8000
backends:
  - url: localhost:8080
  - url: localhost:7070
```

**Staging**:
```yaml
type: least-connection
port: 8000
health_checker_time: 20s
backends:
  - url: staging-1.internal:8080
    health_check_path: /health
  - url: staging-2.internal:8080
    health_check_path: /health
monitoring:
  port: 8001
  host: localhost
```

**Production**:
```yaml
type: least-response-time
port: 443
host: 0.0.0.0
health_checker_time: 15s

backends:
  - url: prod-1.internal:8080
    health_check_path: /health
    max_conn: 1000
    max_conn_timeout: 10s
    max_conn_duration: 60s

server:
  http_version: http1
  cert_file: /etc/divisor/certs/server.crt
  key_file: /etc/divisor/certs/server.key
  read_timeout: 30s
  write_timeout: 30s

custom_headers:
  x-forwarded-for: $remote_addr
  x-request-id: $uuid

monitoring:
  port: 8001
  host: 127.0.0.1
```

## Running Examples

```bash
# Use specific config file (requires absolute path)
divisor --config /absolute/path/to/basic.config.yaml

# Default behavior (looks for config.yaml in current directory)
cp basic.config.yaml config.yaml
divisor
```

## Testing Configurations

1. **Validate syntax**:
   ```bash
   divisor --config /path/to/your.config.yaml
   # Check startup logs for errors
   ```

2. **Check backend health**:
   ```bash
   # Start divisor, then check monitoring endpoint
   curl http://localhost:8001/stats
   ```

3. **Test load balancing**:
   ```bash
   # Send multiple requests and check distribution
   for i in {1..10}; do curl http://localhost:8000/; done
   ```

## Need Help?

- See [main documentation](../README.md#configuration) for detailed field reference
- Check [Custom Middleware guide](../README.md#custom-middleware) for middleware development
- Review algorithm implementations in [`core/`](../core/) directory
