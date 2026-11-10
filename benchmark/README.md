# Release benchmark

Every stable divisor release is measured on one machine, in one run, and the
results can appear on its release page. This directory holds everything that
run uses: the backend every proxy forwards to, each proxy's config, the load
generator pin, and the tool that runs it all and writes the results.

Each proxy measured is called a **Contender**: the divisor being benchmarked
and [Caddy, nginx and Traefik](#contenders), each configured to do the same
work.

## What is measured

Every Contender serves the same two traffic shapes, the **Workloads**, sent by
the [oha](https://github.com/hatoo/oha) load generator, pinned in
[`loadgenerator/Dockerfile`](loadgenerator/Dockerfile):

| Workload | Traffic | Reported |
| --- | --- | --- |
| Throughput | 64 keep-alive connections, each sending its next request as soon as the last one is answered | requests per second, p50 and p99 latency, peak memory |
| Fixed-rate latency | a constant 5,000 requests per second over 64 keep-alive connections, well below any Contender's maximum | p50, p99 and p99.9 latency |

Both send `GET /` without a body over HTTP/1.1 without TLS. Fixed-rate latency
uses oha's coordinated-omission correction: a request's latency counts from
when it was due to be sent, so a stall shows up in the percentiles instead of
quietly lowering the request rate. Requests still in flight when a measurement
ends are waited for, not cut off.

**Peak memory** is the highest memory use of the Contender's container since
it started: the cgroup v2 `memory.peak` file, read in every round right after
the Throughput measurement. It covers the readiness check, the warm-up and the
Throughput Workload, the heavier of the two, which runs first. It counts
everything the kernel charges to the container, not only the proxy's own
allocations: kernel buffers such as socket buffers, and the page cache of
files the container is the first to read, such as its own binary when the
machine has not cached it yet, which can make a round several times the usual
peak. So before round 1 the run starts each Contender once, waits until it is
ready, and stops it, unmeasured: every measured round, a smoke run's single
round included, finds the Contender's files already cached.
A helper container reads the file from the host's cgroup tree, since the
divisor image has no shell. A MB is 2^20 bytes (a MiB), as in Docker's
`--memory 512m` and the 512 MB each Contender gets, in the table and in
`benchmark.json` (`peak_memory_mb`). `memory.peak` needs cgroup v2 and Linux
5.19 or later: when a round could not read it, the table shows n/a with a note
under it, and the run goes on.

The schedule:

- **3 rounds.** In each round every Contender, one after another, is started,
  made ready, run through both Workloads and stopped. Interleaving the rounds
  means a noisy minute on a shared machine hits every Contender instead of one.
- **Warm-up.** Each Workload first runs for 5 seconds, which are thrown away,
  then for 30 seconds, which are measured.
- **Median.** Every reported number is the median of the 3 rounds. The spread,
  (max − min) / median over the rounds, is recorded next to it in
  `benchmark.json`.
- **Ready** means a request through the Contender answered 2xx. A Contender
  that is not ready within 30 seconds, answers anything but 2xx, or drops a
  connection during a measurement gets a failed row instead of numbers, with
  the reason under the table, and the run exits non-zero once its results are
  written.

The machine:

- Every container shares the host network.
- CPU pinning, sized for the 4-vCPU GitHub-hosted runner the release benchmark
  runs on: oha gets CPUs 0 and 1, the Contender CPU 2 with 512 MB of memory
  and no swap, and the four backends share CPU 3. One core per Contender makes
  the numbers read per core, and keeps oha and the backends from being what
  limits a Contender.
- The backends are four instances of a purpose-built server,
  [`backend/`](backend/). It answers every request on any path, health checks
  included, with 200 and the same 1 KB body, and does nothing else.
- The section above the tables names the runner image, the architecture and
  the CPU model.

Absolute numbers depend on the machine, so they compare only within one
release page: rows from one run are comparable, numbers on two release pages
are not, because GitHub's shared runners change CPU model and neighbors
between runs.

## Contenders

Each table ranks the Contenders: Throughput by requests per second, highest
first, and Fixed-rate latency by p50, lowest first. A Contender that failed
comes last. Each row shows the Contender's exact version.

Each Contender is a directory under [`contenders/`](contenders/): a Dockerfile
whose `FROM` line pins the proxy's official image to an exact version, plus
that proxy's config. The `FROM` line is the only place a version is written:
the Version column is read from it, so the compared versions change only when
a pull request changes that line. Once a month, one Dependabot pull request
bumps the Caddy, nginx, Traefik and oha pins together.

### The same work for every Contender

Every Contender is configured to do the same work per request, in its own
config language, and nothing more. Everything not listed here stays at each
proxy's defaults: no compression, no caching, no kernel or buffer settings.

- **Round-robin over the 4 backends**, each with the same weight, so every
  Contender spreads the same load the same way.
- **Keep-alive to the backends.** Every Contender reuses its connections to
  the backends instead of opening one per request. A proxy that reconnected on
  every request would mostly measure TCP handshakes.
- **`X-Forwarded-For` set.** Every Contender tells the backend the client's
  address, the header almost every deployment needs, so every Contender pays
  for adding it.
- **Logging off.** No access log on any Contender: writing a line per request
  costs more than some proxies spend on the request itself, and the proxies
  would differ in what and where they write.
- **One worker.** Each Contender has one core, so each runs one worker process
  or thread on it. A second one would only compete with the first for the
  same core.
- **Health checks every 30 seconds**, where the proxy has them: the backends
  are checked as in production, at a rate too low to show in the numbers.

The smoke run ([Checking the setup](#checking-the-setup)) checks the even
spread, the keep-alive and `X-Forwarded-For` for every Contender instead of
assuming them.

### divisor

divisor runs as its release image. A release is measured from its published
image `ghcr.io/aaydin-tr/divisor:<tag>`, never rebuilt. A branch, or your
checkout, is built into a static `linux/amd64` binary in the repository's
release [`Dockerfile`](../Dockerfile). Then
[`contenders/divisor/Dockerfile`](contenders/divisor/Dockerfile) adds the
benchmark config, [`contenders/divisor/config.yaml`](contenders/divisor/config.yaml):

- `type: round-robin` over the four backends.
- HTTP/1.1 without TLS, on port 8080.
- `health_checker_time: 30s`: active health checks every 30 seconds.
- The access log off, no middlewares and no custom headers.
- One worker: divisor is a Go program, and Go runs as many threads at once as
  the CPUs it may use, here one.
- Response batching on, divisor's default on 2 CPUs or fewer
  (`server.disable_response_batching` is not set): after each response divisor
  lets the other ready requests run before it writes, so their responses go
  out together. It is measured to raise requests per second for a load
  generator holding every connection and for independent single-connection
  clients alike ([`docs/configuration.md`](../docs/configuration.md#connections)).
- Keep-alive to the backends and `X-Forwarded-For` are what divisor always
  does.
- `monitoring.disabled: true`: divisor opens no monitoring server
  (`localhost:8001` by default), which is not proxying, like Caddy's admin
  endpoint and Traefik's API, both off.

### Caddy

The official `caddy` image, pinned in
[`contenders/caddy/Dockerfile`](contenders/caddy/Dockerfile), with
[`contenders/caddy/Caddyfile`](contenders/caddy/Caddyfile):

- One site on port 8080, HTTP/1.1 without TLS, whose `reverse_proxy` sends
  every request to the four backends with `lb_policy round_robin` (Caddy's
  default is random).
- `health_uri /` with `health_interval 30s`: a health check of every backend
  every 30 seconds.
- No `log` directive, so Caddy writes no access log.
- `X-Forwarded-For` at its default: Caddy sets it.
- Keep-alive to the backends at its default: Caddy keeps up to 32 idle
  connections open to each backend, more than the Throughput Workload's 64
  client connections need over four backends.
- One worker: Caddy is a Go program, and Go runs as many threads at once as
  the CPUs it may use, here one.
- `auto_https off` and `admin off`: Caddy manages no certificates and opens no
  admin endpoint (`localhost:2019` by default), neither of which is proxying
  work.

### nginx

The official `nginx` image, pinned in
[`contenders/nginx/Dockerfile`](contenders/nginx/Dockerfile), with
[`contenders/nginx/nginx.conf`](contenders/nginx/nginx.conf) replacing the
image's default config:

- `worker_processes 1`: one worker.
- An `upstream` of the four backends (round-robin is nginx's default) with
  `keepalive 64`: up to 64 idle connections to the backends stay open, as many
  as the Throughput Workload's 64 client connections, so nginx never has to
  close one only to open another.
- `proxy_http_version 1.1` and `proxy_set_header Connection ""`: backend
  keep-alive needs HTTP/1.1 and no `Connection: close` header. Both are
  nginx's defaults since 1.29.7, and stay in the config so it means the same
  on any version.
- `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`: appends the
  client's address to `X-Forwarded-For`.
- `access_log off`.
- No health checks: open source nginx has no active ones.

### Traefik

The official `traefik` image, pinned in
[`contenders/traefik/Dockerfile`](contenders/traefik/Dockerfile), with a
static config, [`contenders/traefik/traefik.yml`](contenders/traefik/traefik.yml),
and the routing it reads through the file provider,
[`contenders/traefik/dynamic.yml`](contenders/traefik/dynamic.yml):

- One entry point on port 8080, and one router sending every request to one
  service over the four backends, each at the default weight (weighted
  round-robin with equal weights is round-robin).
- `healthCheck` with `interval: 30s`: a health check of every backend every 30
  seconds.
- The access log, the API, the dashboard and metrics at their default: off.
- `X-Forwarded-For` at its default: Traefik sets it.
- Keep-alive to the backends at its default: Traefik keeps idle backend
  connections open for reuse.
- One worker: Traefik is a Go program, and Go runs as many threads at once as
  the CPUs it may use, here one.
- `global.checkNewVersion: false`: otherwise Traefik asks traefik.io for a
  newer release, which is not proxying work.

## Running it locally

You need Docker on Linux, or Docker Desktop on macOS or on Windows with WSL2,
with at least 4 CPUs available to it; Go; and git. From the repository root:

```bash
make benchmark
```

It builds the checkout, pulls the published images of the other Contenders,
runs the full schedule (about 4 minutes per Contender), prints the Markdown
section and writes the results to `benchmark/results/`, which git ignores:

| File | Contents |
| --- | --- |
| `section.md` | the Markdown section that was printed |
| `benchmark.json` | the run's metadata, every Contender's version and image, and per Workload and Contender each round's numbers, the median, the spread and the status |
| `recording.json` | what the run observed; `section.md` and `benchmark.json` are made from it and the raw outputs |
| `raw/round-N/<contender>/` | oha's JSON output for each Workload, and the Contender's log |

A local run never publishes anything. To render `section.md` and
`benchmark.json` again from saved results:

```bash
go -C benchmark run ./cmd/release-benchmark report --results results
```

Local numbers only say something about your machine. On Docker Desktop:

- Pinned CPUs are virtual CPUs of Docker Desktop's VM, which your operating
  system schedules freely, so pinning isolates less than on Linux.
- On Apple silicon the `linux/amd64` divisor image runs under emulation, so
  its numbers say nothing about divisor.
- Containers on the host network reach each other, but your machine reaches
  them only when Docker Desktop's host networking setting is on. The benchmark
  does not need it: readiness is checked from a container on the host network.

## Checking the setup

The numbers only compare proxies fairly if every Contender really does the
same work. A short smoke run checks that, instead of assuming it:

```bash
make benchmark-smoke
```

It runs every Contender for 1 round, without warm-up, for about 3 seconds per
Workload, and first sends 2,000 requests through it over 8 keep-alive
connections. Each backend tags its answer with its name, the connection the
request arrived on, and whether `X-Forwarded-For` was set, and the smoke run
checks, for every Contender, that:

- every request answered 2xx, in those 2,000 requests and in both Workloads;
- each of the 4 backends answered between 80% and 120% of an even share;
- every request reached its backend with `X-Forwarded-For`;
- each backend saw at most one connection per 10 requests it answered, so the
  Contender keeps its backend connections alive instead of reconnecting.

A failed check prints the Contender and the check, and the run exits
non-zero. The answers it checked are in
`benchmark/results/raw/round-1/<contender>/equivalent-setup-sample.json`. A smoke
run is too short for its numbers to mean anything, and its section says so.
