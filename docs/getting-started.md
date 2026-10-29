# Getting started

This guide takes you from nothing to divisor balancing traffic across two
Backends, then shows what happens when one of them goes Down. It takes about
five minutes.

You need [Docker](https://docs.docker.com/get-docker/) to run the two demo
Backends ([traefik/whoami](https://github.com/traefik/whoami), a tiny server
that answers with its own hostname). Divisor itself runs either as a plain
binary or as a container; pick one below.

## 1. Install divisor

Pick whichever suits you:

- **Release binary.** Every release attaches archives for Linux, macOS and
  Windows to the [releases page](https://github.com/aaydin-tr/divisor/releases).
  On Linux x86-64:

  ```bash
  curl -fsSL https://github.com/aaydin-tr/divisor/releases/latest/download/divisor_Linux_x86_64.tar.gz | tar -xz divisor
  sudo mv divisor /usr/local/bin/
  ```

  Other archives follow the same `divisor_<Os>_<arch>` naming
  (`divisor_Darwin_arm64.tar.gz`, `divisor_Windows_x86_64.zip`, ...).
- **`go install`** (needs Go), which puts the binary in `$(go env GOPATH)/bin`:

  ```bash
  go install github.com/aaydin-tr/divisor@latest
  ```

- **From source**:

  ```bash
  git clone https://github.com/aaydin-tr/divisor.git
  cd divisor
  go build -o divisor
  ```

- **Docker image**: `ghcr.io/aaydin-tr/divisor` (also on Docker Hub as
  `aaydintr/divisor`). Nothing to install; skip to
  [Run divisor in Docker](#run-divisor-in-docker). The
  [Docker guide](deployment/docker.md) covers the image in depth.
- **Kubernetes**: a Helm chart and plain manifests; see
  [Kubernetes and Helm](deployment/kubernetes.md).

Check the binary with `divisor --version`.

## 2. Run divisor as a binary

Start two Backends, published on `localhost:9001` and `localhost:9002`:

```bash
docker run -d --name whoami-1 --hostname whoami-1 -p 9001:80 traefik/whoami
docker run -d --name whoami-2 --hostname whoami-2 -p 9002:80 traefik/whoami
```

The config, [`examples/quickstart-local.config.yaml`](../examples/quickstart-local.config.yaml),
is all divisor needs. It sets three keys:

- `port: 8080` is where divisor listens for clients (on every interface, `0.0.0.0`,
  unless you set `host`).
- `backends` lists the Backends, `localhost:9001` and `localhost:9002`, each as
  `host:port`; divisor always speaks plain HTTP to them.
- `health_checker_time: 5s` is how often divisor Probes each Backend. The default
  is `30s`; `5s` makes the demo below quicker.
- No `type`, so the Balancer is `round-robin`.

Download it and start divisor, giving `--config` an absolute path (without the
flag divisor reads `./config.yaml` from the directory it was started in):

```bash
curl -fsSLo divisor.yaml https://raw.githubusercontent.com/aaydin-tr/divisor/main/examples/quickstart-local.config.yaml
divisor --config "$PWD/divisor.yaml"
```

Divisor Probes both Backends once before it starts serving, and logs each one
as Alive. Logs are JSON on stderr; add `logging: {format: console}` to the
config for a human-readable format.

Skip to [Send requests](#3-send-requests).

### Run divisor in Docker

The container reaches the Backends by name over a Docker network, so the
config, [`examples/quickstart.config.yaml`](../examples/quickstart.config.yaml),
lists the Backends as `whoami-1:80` and `whoami-2:80`. It also sets
`monitoring.host: 0.0.0.0`: the Monitoring server binds `localhost` by
default, which is unreachable from outside a container, so the config binds it
on `0.0.0.0` and the command below publishes it on `127.0.0.1` only.

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

The image reads its config from `/etc/divisor/config.yaml`. Follow its logs
with `docker logs -f divisor`.

## 3. Send requests

```bash
for i in 1 2 3 4; do curl -s localhost:8080 | grep Hostname; done
```

```text
Hostname: whoami-2
Hostname: whoami-1
Hostname: whoami-2
Hostname: whoami-1
```

Each request went to the next Backend in turn. The whole response is whoami
echoing the request it received, including the `X-Forwarded-For` header
divisor added with your Client IP.

## 4. Watch the Pool

The Monitoring server, on `localhost:8001` by default, has a dashboard at
<http://localhost:8001> and the same data as JSON:

```bash
curl -s localhost:8001/stats
```

Each Backend has a row with its request count, average response time and
`is_host_alive`. [Observability](observability.md) covers the Monitoring
server, Prometheus metrics and logs.

Now stop a Backend:

```bash
docker stop whoami-2
```

Within one Probe interval divisor logs `Backend is Down, leaving rotation`
and every request goes to `whoami-1` (one sent before the Probe notices may
get a `502`). Start it again:

```bash
docker start whoami-2
```

The next successful Probe logs `Backend is Alive again, Rejoining rotation`
and requests alternate again. With every Backend Down divisor keeps running
and answers `503` until one Rejoins.

## Clean up

```bash
docker rm -f divisor whoami-1 whoami-2
docker network rm divisor-quickstart   # only if you ran divisor in Docker
```

## Next steps

- Pick a different Balancer, tune timeouts, add TLS or HTTP/2:
  [Configuration](configuration.md), and the ready-made configs in
  [`examples/`](../examples/)
- Change the Backend list without a restart: Reload, in
  [Configuration](configuration.md)
- Rewrite requests or answer them yourself: [Middleware](middleware.md)
- Run it for real: [Docker](deployment/docker.md),
  [Kubernetes and Helm](deployment/kubernetes.md),
  [systemd](deployment/systemd.md)
- Upgrading from 0.x: [Migrating to 1.0](migration-1.0.md)
