# Divisor Configuration Examples

This directory contains example configurations for different use cases and deployment scenarios.

## Quick Start

**Quickstart** - The [README quickstart](../README.md#quickstart) and [Getting started](../docs/getting-started.md):
- [`quickstart.config.yaml`](quickstart.config.yaml) - divisor in Docker in front of two whoami Backends
- [`quickstart-local.config.yaml`](quickstart-local.config.yaml) - the same, with divisor as a binary on the host

**Minimal setup**:
- [`basic.config.yaml`](basic.config.yaml) - Simplest possible configuration

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

**Kubernetes**:
- [`kubernetes/`](kubernetes/) - Deployment, ConfigMap and Service for divisor fronting the pods of a headless Service; probes on `/healthz`/`/ready`, and a ConfigMap edit of the Backend list Reloads without a redeploy (`kubectl apply -f examples/kubernetes/`)

**systemd**:
- [`systemd/`](systemd/) - A hardened `divisor.service` unit and the `config.yaml` it starts on; installation and logs in [systemd](../docs/deployment/systemd.md)

**Observability**:
- [`observability.config.yaml`](observability.config.yaml) - Access log on, Monitoring server reachable by Prometheus
- [`observability/`](observability/) - A Docker Compose stack (divisor, two Backends, Prometheus, Loki, Grafana Alloy) that runs that config; walked through in [Observability](../docs/observability.md)

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

Every divisor config here (each `*.config.yaml`, `systemd/config.yaml`, and
the config inside `kubernetes/configmap.yaml`) uses only keys divisor's strict
config parser accepts, so any of them is a safe starting point.

- **Development**: [`basic.config.yaml`](basic.config.yaml) - round-robin over
  local Backends, every other key at its default
- **Staging**: [`least-connection.config.yaml`](least-connection.config.yaml) -
  Probes on `/health` every 20s, tighter timeouts, monitoring on localhost
- **Production**: [`production.config.yaml`](production.config.yaml) - TLS,
  tuned Backend connection pools, Access log on, bounded proxy timeout
- **Kubernetes**: [`kubernetes/configmap.yaml`](kubernetes/configmap.yaml) -
  short `server.dns_cache_duration` for churning pod IPs, monitoring on
  `0.0.0.0` for kubelet probes

Swap `type:` in any of them to change the Balancer; the
[configuration reference](../docs/configuration.md) lists every key.

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

- See the [configuration reference](../docs/configuration.md) for every field
- Check the [Middleware guide](../docs/middleware.md) for middleware development
- Deploying: [Docker](../docs/deployment/docker.md), [Kubernetes and Helm](../docs/deployment/kubernetes.md), [systemd](../docs/deployment/systemd.md)
- Review algorithm implementations in [`core/`](../core/) directory
