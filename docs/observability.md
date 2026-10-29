# Observability

Divisor gives you three signals:

- **Application logs** on stderr: what divisor itself is doing (startup,
  Backends going Down and Rejoining, Reloads, shutdown).
- **The Access log** on stdout, opt-in: one JSON line per request divisor
  answers.
- **The Monitoring server**, a separate HTTP listener: a dashboard, JSON
  stats, Prometheus metrics and the `/healthz` and `/ready` probe endpoints.

Divisor writes no log files. Each stream carries one kind of line, so you can
route, parse and retain them separately; the
[Loki example](#shipping-logs-to-loki) below does exactly that.

## Application logs

Application logs go to stderr, as JSON by default. `logging.format: console`
switches to a human-readable encoding for local runs, and `logging.level`
sets the minimum level (`info` by default). The keys are in
[Configuration](configuration.md#logging-settings).

A JSON line always has `time` (ISO 8601 with milliseconds and the host's UTC
offset; `Z` in the official image), `level` and `msg`; some lines add fields:

```json
{"level":"warn","time":"2026-10-29T16:45:46.067Z","msg":"All backends are down, serving 503 until a backend rejoins"}
{"level":"info","time":"2026-10-29T16:46:02.311Z","msg":"Reload applied","trigger":"SIGHUP","added_backends":["whoami-3:80"],"removed_backends":[],"kept_backends":2,"probe_settings_changed":false}
```

Lines worth alerting on:

| Message | Level | Meaning |
| --- | --- | --- |
| `All backends are down, serving 503 until a backend rejoins` | `warn` | The last Alive Backend went Down; every request gets `503` until one Rejoins |
| `No Backend is Alive at startup; serving 503 until a Probe succeeds` | `warn` | Divisor started before its Backends; they Rejoin as Probes succeed |
| `Backend is Down, leaving rotation, Addr: ...` | `info` | A Probe failed for an Alive Backend |
| `Backend is Alive again, Rejoining rotation, Addr: ...` | `info` | A Down Backend Rejoined |
| `Reload rejected, divisor keeps serving the running config` | `error` | A Reload was refused; its `error` field names why (see [Reload](configuration.md#reload)) |
| `Graceful shutdown timeout reached, forcing shutdown` | `warn` | Shutdown hit `server.graceful_shutdown_timeout`; divisor exits `1` |

At `debug` level divisor also logs every Streamed response's end
(`Streamed response ended`, with its `backend`, `cause` and
`forwarded_bytes`).

## Access log

Turn it on with `logging.access_log: true`. Divisor then writes one line per
request it answers to stdout, and nothing else ever goes to stdout. The
Access log is always JSON, whatever `logging.format` says: it is a machine
stream. Off, it costs the request path one disabled-check.

```json
{"time":"2026-10-29T16:44:56.444Z","client_ip":"172.20.0.1","method":"GET","path":"/hello","status":200,"backend":"whoami-1:80","request_seq":2,"duration_ms":0.597997,"bytes_out":198}
{"time":"2026-10-29T16:45:12.020Z","client_ip":"172.20.0.1","method":"GET","path":"/admin","status":403,"backend":"whoami-2:80","request_seq":7,"duration_ms":0.031,"bytes_out":21,"short_circuit":true}
{"time":"2026-10-29T16:45:47.134Z","client_ip":"172.20.0.1","method":"GET","path":"/down","status":503,"duration_ms":0,"bytes_out":35}
```

A line is written when the response is ready: for a buffered response, once
divisor has it in full; for a [Streamed response](configuration.md#streamed-responses),
when the stream ends, however it ends (the Backend finished, the stream sat
idle past `server.stream_idle_timeout`, the client disconnected or stopped
reading). An open stream has no line yet, and its one line covers the whole
stream.

Requests the server rejects before divisor routes them get no line: a body
over `server.max_request_body_size` (`413`), a malformed request (`400`), an
oversized header (`431`) or a read timeout (`408`).

### Access log fields

| Field | Type | Present | Meaning |
| --- | --- | --- | --- |
| `time` | string | always | When the line was written, RFC 3339 in UTC with milliseconds (`2026-10-29T16:44:56.444Z`) |
| `client_ip` | string | always | The Client IP: the TCP peer that opened the connection. A client-sent `X-Forwarded-For` is ignored; behind another proxy this is that proxy |
| `method` | string | always | The method as the client sent it; a Middleware rewrite is not logged |
| `path` | string | always | The path exactly as the client sent it, without the query string: no percent-decoding or normalizing (`/a//b/../c%20d?q=1` logs `/a//b/../c%20d`); a Middleware rewrite is not logged |
| `status` | number | always | The status divisor sent: the Backend's, `502` for a Backend that failed or was unreachable, `504` when `server.proxy_timeout` expired, `500` when a Middleware returned an error, a Short-circuit's own status, or `503` with no Alive Backend. A Streamed response cut short keeps the status it started with |
| `backend` | string | omitted only on the `503` no-Alive-Backend line | The Backend address that served the request, or that a Middleware short-circuited (a Short-circuit from `OnRequest` still names the Backend that was picked) |
| `request_seq` | number | with `backend` | The Request sequence number: the request's position among all requests routed to that Backend since start, the value its `$incremental` custom header carries. A short-circuited request consumes one too |
| `duration_ms` | number | always | Milliseconds from the request reaching divisor's proxy to the response being ready, Middlewares included; for a Streamed response, until the stream ended. `0` on the `503` no-Alive-Backend line |
| `bytes_out` | number | always | Response body bytes, headers excluded; for a Streamed response, the bytes forwarded to the client before it ended. A body a Middleware sets as a stream logs `0` |
| `short_circuit` | bool | only when a Middleware answered | Always `true`: a Middleware [Short-circuited](middleware.md#short-circuiting) the request, from `OnRequest` or `OnResponse` |

A Middleware replacing a Streamed response before its first body byte is a
Short-circuit: the line describes the Middleware's response, and the Backend
stream it closed gets no line of its own.

## Monitoring server

A separate HTTP server, `localhost:8001` by default (`monitoring.host`,
`monitoring.port`; see [Configuration](configuration.md#monitoring-settings)).
In a container, `localhost` is unreachable from outside: bind `0.0.0.0` and
publish the port only where it should be reachable, since none of these
endpoints is authenticated.

| Path | Serves |
| --- | --- |
| `/` | A dashboard: CPU and RAM usage, goroutines, open connections, and a row per Backend |
| `/stats` | The same data as JSON, read at request time |
| `/metrics` | Prometheus metrics, refreshed every 5 seconds |
| `/healthz` | Liveness: `200` whenever the process runs |
| `/ready` | Readiness: `200` once the listeners are bound, `503` from the moment graceful shutdown begins |

Neither probe endpoint depends on Backend liveness: with every Backend Down
divisor is still working, answering `503`, and restarting it would not help.
[Kubernetes](deployment/kubernetes.md) wires them as liveness and readiness
probes.

### `/stats`

```json
{
  "backends": [
    {"addr": "whoami-1:80", "total_req_count": 3, "avg_res_time": 1.667, "last_use_time": "2026-10-29T16:44:56Z", "conns_count": 1, "is_host_alive": true, "backend_hash": 0}
  ],
  "memory": {"process_percent": 0.16, "total_percent": 9.31, "process_mb": 26.43},
  "cpu": {"process_percent": 0.39, "total_percent": 1.16},
  "total_goroutine": 20,
  "open_conn_count": 4
}
```

`backends` has one row per Backend, in config order, Down Backends included;
the fields mean the same as the `backend_*` metrics below. `backend_hash` is
the Backend's Index: its config position at boot, a number never used before
for a Backend a Reload added. `last_use_time` is the last time divisor used a
connection to the Backend.

### Prometheus metrics

`/metrics` serves these gauges, refreshed from the same data every 5 seconds,
so scraping more often only repeats samples:

| Metric | Labels | Meaning |
| --- | --- | --- |
| `backend_alive` | `address`, `index` | `1` while the Backend is Alive, `0` while it is Down |
| `backend_total_request_count` | `address`, `index` | Requests routed to the Backend since start, failed and short-circuited ones included (its latest `request_seq`) |
| `backend_average_response_time` | `address`, `index` | Lifetime average response time of the Backend's successful requests, in milliseconds; a Streamed response counts its time to the first response byte |
| `backend_connection_count` | `address`, `index` | Open connections from divisor to the Backend, busy and idle |
| `open_conn_count` | | Open client connections on the HTTP/1.1 listener; always `0` with `server.http_version: http2` |
| `total_goroutine` | | Goroutines in the divisor process |
| `process_cpu_percent` | | CPU usage of the divisor process, in percent |
| `total_cpu_percent` | | CPU usage of the whole host, in percent |
| `process_memory_mb` | | Memory used by the divisor process, in MB |
| `process_memory_percent` | | Memory used by the divisor process, in percent of the host's |
| `total_memory_percent` | | Memory used on the whole host, in percent |

The `address` label is the Backend address and `index` its Index, the
number `/stats` shows as `backend_hash`, so each Backend has its own series,
even two listing the same address. When a Reload removes a Backend, its
series disappear. The endpoint also serves the Prometheus Go client's standard
`go_*` runtime metrics, `process_*` metrics (on Linux) and `promhttp_*`
metrics about `/metrics` itself.

`backend_total_request_count` is a gauge that only grows for the life of the
process, so `rate()` works on it (Prometheus 3 annotates the query because
the name does not end in `_total`). Some starting points:

```promql
# A Backend is Down
backend_alive == 0

# Every Backend is Down: divisor is answering 503
sum(backend_alive) == 0

# Requests per second per Backend
rate(backend_total_request_count[5m])

# Requests per second per Backend address, summing Backends that share one
sum by (address) (rate(backend_total_request_count[5m]))
```

A minimal scrape config is
[`examples/observability/prometheus.yml`](../examples/observability/prometheus.yml).

## Shipping logs to Loki

[`examples/observability/`](../examples/observability/) runs divisor in Docker
Compose with two Backends, [Grafana Alloy](https://grafana.com/docs/alloy/)
shipping its logs to Loki, and Prometheus scraping `/metrics`:

| File | Role |
| --- | --- |
| [`examples/observability.config.yaml`](../examples/observability.config.yaml) | The divisor config: Access log on, Monitoring server on `0.0.0.0:8001` |
| [`examples/observability/compose.yaml`](../examples/observability/compose.yaml) | divisor, two `traefik/whoami` Backends, Loki, Alloy, Prometheus |
| [`examples/observability/config.alloy`](../examples/observability/config.alloy) | The log pipeline |
| [`examples/observability/prometheus.yml`](../examples/observability/prometheus.yml) | The scrape config |

```bash
cd examples/observability
docker compose up -d
curl http://localhost:8080/hello
```

The pipeline keys on the stdout/stderr split. Docker keeps the two streams
apart, and Alloy's `loki.source.docker` exposes which one a line came from as
`__meta_docker_container_log_stream`; `config.alloy` turns that into a `log`
label, so the Access log and the Application logs land in Loki as separate
streams:

| Container stream | Loki stream | Pipeline |
| --- | --- | --- |
| stdout | `{service="divisor", log="access"}` | Line timestamp taken from `time` |
| stderr | `{service="divisor", log="app"}` | Line timestamp taken from `time`; `level` becomes a label |

Access log fields stay in the line rather than becoming labels (`client_ip`
and `path` would explode Loki's stream count); LogQL's `json` parser reads
them at query time. Query Loki (`http://localhost:3100`, or through Grafana)
with, for example:

```logql
# Every request that failed
{service="divisor", log="access"} | json | status >= 500

# Requests per status over the last 5 minutes
sum by (status) (count_over_time({log="access"} | json [5m]))

# p99 duration per Backend
quantile_over_time(0.99, {log="access"} | json | unwrap duration_ms [5m]) by (backend)

# Application warnings and errors
{service="divisor", log="app", level=~"warn|error"}
```

The pipeline expects JSON Application logs, the default; with
`logging.format: console` the `log="app"` stream still arrives, unparsed.

### Other platforms

The same split works wherever the platform keeps stdout and stderr apart.

- **Kubernetes**: `kubectl logs` and the pod log API merge the two streams,
  so a collector reading through the API (Alloy's `loki.source.kubernetes`)
  cannot tell them apart. Tail the node's pod log files instead
  (`/var/log/pods/*/*/*.log`, Alloy's `local.file_match` and
  `loki.source.file` on a DaemonSet): the container runtime writes each line
  with its stream, and `stage.cri {}` in `loki.process` extracts it as
  `stream`, which `stage.labels { values = {stream = ""} }` makes a label to
  match `{stream="stdout"}` on.
- **systemd**: journald keeps both streams under the unit without recording
  which one a line came from. Tell them apart by shape (below), or give the
  Access log its own destination with `StandardOutput=` in the unit (see
  [Running divisor with systemd](deployment/systemd.md)).
- **Anywhere the stream is lost**, tell the lines apart by shape: an Access
  log line never has a `level` or `msg` key, and a JSON Application log line
  always has both.
