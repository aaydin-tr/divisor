# Running divisor in Docker

Every divisor release publishes an official image. This guide covers pulling
it, mounting a config, publishing ports, Reloading a running container and
reading its two log streams.

## The image

The same multi-arch image (`linux/amd64` and `linux/arm64`) is published to
two registries:

- GHCR: `ghcr.io/aaydin-tr/divisor`
- Docker Hub: `aaydintr/divisor`

| Tag | Points at | Use it for |
| --- | --- | --- |
| `v1.0.0` | that release, never moves | production: reproducible deployments |
| `v1.0` | the latest `v1.0.x` patch release | taking patch fixes automatically |
| `latest` | the latest release | trying divisor out |

Prereleases (`v1.1.0-rc.1`) get only their own tag, never `v1.1` or `latest`.

```bash
docker pull ghcr.io/aaydin-tr/divisor:v1.0.0
docker run --rm ghcr.io/aaydin-tr/divisor:v1.0.0 --version
```

The image is built on distroless (`gcr.io/distroless/static-debian13:nonroot`):
it holds the divisor binary and CA certificates, nothing else. There is no
shell, so `docker exec` has nothing to run, and divisor runs as the nonroot
user, UID/GID `65532`. Its entrypoint is `/divisor` and its default command is
`--config /etc/divisor/config.yaml`; arguments you pass replace the command,
as `--version` does above.

## Run it

Divisor reads its config from `/etc/divisor/config.yaml`. Keep the config in a
directory of its own and mount the directory there (why a directory and not
the file is under [Reload](#reload)):

```bash
mkdir divisor
# write divisor/config.yaml, e.g. from examples/quickstart.config.yaml
docker run -d --name divisor \
  -p 8080:8080 -p 127.0.0.1:8001:8001 \
  -v "$PWD/divisor:/etc/divisor:ro" \
  --read-only \
  ghcr.io/aaydin-tr/divisor:v1.0.0
```

- The config file must be readable by UID `65532`: a file only its owner can
  read (mode `0600`) stops divisor at startup with `permission denied`. Mode
  `0644` works.
- Divisor writes nothing to disk, so `--read-only` costs nothing.
- Backends are dialed from inside the container: `localhost` there is the
  container itself. Use names on a shared Docker network (as below), or a
  host address.

[`examples/quickstart.config.yaml`](../../examples/quickstart.config.yaml) is a
complete config for this setup; the
[Getting started](../getting-started.md#run-divisor-in-docker) guide runs it
against two demo Backends.

## Ports

- **Client traffic**: divisor listens on `port` on every interface (`host`
  defaults to `0.0.0.0`). The image declares `8080`; keep `port: 8080` and
  publish it on whichever host port you want, e.g. `-p 80:8080`.
- **Monitoring server** (`/healthz`, `/ready`, `/stats`, `/metrics`): it binds
  `localhost:8001` by default, which nothing outside the container can reach.
  Set `monitoring.host: 0.0.0.0` in the config, then publish it on the host's
  loopback only (`-p 127.0.0.1:8001:8001`) unless something else, such as a
  Prometheus on another host, needs it.

The image has no shell or `curl`, so a Docker `HEALTHCHECK` command cannot run
inside it; probe `/healthz` and `/ready` from outside, as Kubernetes does. Both
answer `200` while every Backend is Down: divisor is working then, answering
`503`.

## Docker Compose

```yaml
services:
  divisor:
    image: ghcr.io/aaydin-tr/divisor:v1.0.0
    ports:
      - "8080:8080"
      - "127.0.0.1:8001:8001"
    volumes:
      - ./divisor:/etc/divisor:ro
    read_only: true
    restart: unless-stopped
    # Must exceed server.graceful_shutdown_timeout (default 30s).
    stop_grace_period: 40s
  whoami-1:
    image: traefik/whoami
    hostname: whoami-1
  whoami-2:
    image: traefik/whoami
    hostname: whoami-2
```

With [`examples/quickstart.config.yaml`](../../examples/quickstart.config.yaml)
saved as `divisor/config.yaml`, `docker compose up -d` balances the two
`whoami` Backends.

## Stopping

`docker stop` sends `SIGTERM`: divisor flips `/ready` to `503` first, then
stops accepting connections and waits for in-flight requests, for up to
`server.graceful_shutdown_timeout` (default `30s`). Docker's own stop timeout
is only 10 seconds before it sends `SIGKILL`, so give it more:
`docker stop -t 40 divisor`, or `stop_grace_period` in Compose.

## Reload

A Reload applies an edited config without a restart: only `backends`,
`health_checker_time` and `health_checker_timeout` may change. An edit touching
any other key is rejected as a whole, the log names the keys, and divisor keeps
serving the running config; apply such an edit with `docker restart divisor`.
The rules are in [Configuration](../configuration.md#reload).

Trigger a Reload with `SIGHUP`, which works everywhere:

```bash
docker kill --signal HUP divisor
# or, with Compose
docker compose kill -s HUP divisor
```

Divisor also watches its config file and Reloads about a second after an edit,
but a container only sees edits that reach its filesystem:

- **A mounted directory on a Linux host**: an edit from the host Reloads by
  itself.
- **Docker Desktop (macOS, Windows)**: host file events may not reach the
  container (observed on Docker Desktop for Windows), so an edit may not
  Reload by itself; send `SIGHUP` after each edit, the reliable trigger.
- **A single mounted file** (`-v ./config.yaml:/etc/divisor/config.yaml`):
  never raises an event inside the container. Worse, the mount pins the file
  as it was: an editor that saves by writing a new file and renaming it over
  the old one leaves the container reading the old content, so even `SIGHUP`
  reloads the old config. Mount the directory instead.

Each Reload logs `Reload applied` (with the added and removed Backends) or
`Reload rejected, divisor keeps serving the running config` (with the
reason):

```bash
docker logs divisor 2>&1 | grep Reload
```

## Log streams

Divisor writes two separate streams; see [Observability](../observability.md)
for their content:

- **stderr**: Application logs, JSON by default (`logging.format`,
  `logging.level`).
- **stdout**: the Access log, one JSON line per answered request, only when
  `logging.access_log: true`. Nothing else is ever written to stdout.

`docker logs` keeps the two apart, replaying each on the stream it was written
to:

```bash
docker logs -f divisor 2>/dev/null        # the Access log only
docker logs -f divisor 2>&1 >/dev/null    # Application logs only
```

`docker compose logs` prints both streams to stdout together; use `docker logs`
on the container when you need them apart. Do not start the container with
`-t`: a TTY merges stdout and stderr into one stream.

A log shipper reading container logs gets the stream with every line: the
default `json-file` driver stores it as `"stream": "stdout"` or `"stderr"`, so
the shipper can route the Access log and Application logs to different
destinations.
