# Divisor documentation

New to divisor? Start with [Getting started](getting-started.md).

## Guides

- [Getting started](getting-started.md): install divisor, write a first
  config, send a first request, watch a Backend go Down and Rejoin
- [Configuration](configuration.md): every config key, section by section,
  with its type, default and constraints
- [Middleware](middleware.md): writing a Middleware, the hooks, Short-circuit,
  and the rules every Middleware must follow
- Deployment
  - [Docker](deployment/docker.md): the official image, config mount, ports
    and log streams
  - [Kubernetes and Helm](deployment/kubernetes.md): the example manifests,
    the Helm chart, probes, Reload via ConfigMap
  - [systemd](deployment/systemd.md): running divisor on a traditional host
- [Observability](observability.md): Application logs, the Access log, the
  Monitoring server, Prometheus metrics
- [Migrating to 1.0](migration-1.0.md): every breaking change since 0.x and
  its remedy

## Reference material

- [`examples/`](../examples/): ready-made config files to start from
