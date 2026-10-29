<br />
<div align="center">
  <h3 align="center">Divisor</h3>

  <p align="center">
    A fast and easy-to-configure HTTP load balancer
    <br />
    <a href="docs/getting-started.md"><strong>Getting started</strong></a>
    ·
    <a href="docs/README.md"><strong>Documentation</strong></a>
    ·
    <a href="examples/"><strong>Examples</strong></a>
  </p>
</div>

## What divisor is

Divisor is a layer-7 HTTP load balancer written in Go. Clients send requests to
divisor; divisor picks a Backend with the Balancer you configure and forwards
the request to it. It Probes every Backend, stops sending traffic to one that
goes Down and lets it Rejoin when it recovers, all from one YAML file.

It serves HTTP/1.1 through [fasthttp](https://github.com/valyala/fasthttp) and
HTTP/2 through Go's `net/http`, terminates TLS itself, and always speaks plain
HTTP/1.1 to Backends. It does not balance TCP or UDP, and does not support
HTTP/3, WebSockets or other connection upgrades.

## Features

- **Six Balancers**: `round-robin`, `w-round-robin`, `least-connection`,
  `least-response-time`, `ip-hash` and `random`
- **Health checking**: a Probe per Backend on an interval; Down Backends leave
  the rotation and Rejoin on their first successful Probe. Divisor starts and
  answers `503` even before any Backend is Alive
- **TLS termination and HTTP/2** for clients
- **Reload without restart**: edit the Backend list or Probe settings and send
  `SIGHUP`, or let divisor notice the config file change (Kubernetes ConfigMap
  updates included)
- **Streamed responses** (opt-in): Server-Sent Events and large downloads are
  forwarded as their bytes arrive, on HTTP/1.1 and HTTP/2
- **Middleware in Go**, interpreted at startup: rewrite requests and
  responses, or Short-circuit a request with a response of your own
- **Bounded failure**: `502` for an unreachable Backend, `504` past
  `server.proxy_timeout`, `413` for an oversized request body
- **Observability**: JSON Application logs on stderr, an opt-in Access log on
  stdout, a monitoring dashboard with per-Backend stats, a Prometheus
  `/metrics` endpoint, and `/healthz` and `/ready` for orchestrators
- **Ready to deploy**: a multi-arch distroless Docker image, a Helm chart and
  plain Kubernetes manifests

## Quickstart

With Docker running, this starts two [whoami](https://github.com/traefik/whoami)
Backends and divisor in front of them:

```bash
docker network create divisor-quickstart
docker run -d --name whoami-1 --hostname whoami-1 --network divisor-quickstart traefik/whoami
docker run -d --name whoami-2 --hostname whoami-2 --network divisor-quickstart traefik/whoami

curl -fsSLo divisor.yaml https://raw.githubusercontent.com/aaydin-tr/divisor/main/examples/quickstart.config.yaml
docker run -d --name divisor --network divisor-quickstart \
  -p 8080:8080 -p 127.0.0.1:8001:8001 \
  -v "$PWD/divisor.yaml:/etc/divisor/config.yaml:ro" \
  ghcr.io/aaydin-tr/divisor:latest
```

Send a few requests and watch them alternate between the two Backends:

```bash
for i in 1 2 3 4; do curl -s localhost:8080 | grep Hostname; done
```

The monitoring dashboard is at <http://localhost:8001>. The config is
[`examples/quickstart.config.yaml`](examples/quickstart.config.yaml);
[Getting started](docs/getting-started.md) walks through it, runs divisor as a
plain binary instead, and shows a Backend going Down and Rejoining.

## Documentation

| Guide | What it covers |
| --- | --- |
| [Getting started](docs/getting-started.md) | Installing, a first config, a first request |
| [Configuration](docs/configuration.md) | Every config key with its default and constraints |
| [Middleware](docs/middleware.md) | Writing a Middleware: hooks, Short-circuit, the rules it must follow |
| [Docker](docs/deployment/docker.md), [Kubernetes and Helm](docs/deployment/kubernetes.md), [systemd](docs/deployment/systemd.md) | Running divisor in production |
| [Observability](docs/observability.md) | Logs, the Access log, the Monitoring server and Prometheus |
| [Migrating to 1.0](docs/migration-1.0.md) | Every breaking change and what to do about it |

The full index is
[`docs/README.md`](docs/README.md). Ready-made configs are in
[`examples/`](examples/).

## Benchmark

See the [benchmark folder](benchmark/) for the setup and results.

## Contributing

To build, test or change divisor, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Contributors

<a href = "https://github.com/aaydin-tr/divisor/graphs/contributors">
  <img src = "https://contrib.rocks/image?repo=aaydin-tr/divisor"/>
</a>

## License

Divisor is licensed under the MIT License; see [LICENSE](LICENSE).
