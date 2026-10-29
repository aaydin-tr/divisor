# Configuration

Divisor reads one YAML file. This page lists every key it accepts, section by
section. Ready-made configs live in [`examples/`](../examples/), and
[`advanced.config.yaml`](../examples/advanced.config.yaml) sets every key
outside `middlewares` with a comment (`backends[].health_check_status`
commented out); [`middleware.config.yaml`](../examples/middleware.config.yaml)
declares Middlewares.

Each table gives, per key:

- **Type**: `string`, `int`, `bool`, `list`, `map`, or `duration`, a Go
  duration string such as `500ms`, `30s`, `5m` or `1h30m`
- **Default**: the value used when the key is absent; `required` keys have none
- **Since**: the divisor release that introduced the key
- **Reload**: `yes` when a [Reload](#reload) applies a change to the key;
  `no` when the change needs a restart

## Command-line flags

| Flag | Description | Default |
| --- | --- | --- |
| `--config` | Path to the config file; use an absolute path | `./config.yaml` |
| `--version` | Print the version and exit | - |

## Minimal config

`port` and at least one Backend are the only required keys;
[`basic.config.yaml`](../examples/basic.config.yaml) is a complete config with
little more than that.

## Top-level settings

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `port` | string | required | 0.1.0 | no | Port divisor listens on for clients. A number such as `8000` is fine |
| `host` | string | `0.0.0.0` | 0.1.0 | no | Address divisor listens on for clients. The default accepts traffic from other hosts; the Monitoring server stays on `localhost` |
| `type` | string | `round-robin` | 0.1.0 | no | The Balancer: `round-robin`, `w-round-robin`, `ip-hash`, `random`, `least-connection` or `least-response-time`; any other value is a startup error. `w-round-robin` with a single Backend runs as `round-robin` |
| `health_checker_time` | duration | `30s` | 0.1.0 | yes | Interval between Probe rounds; each round Probes every Backend, Alive and Down. `0` or negative means the default |
| `health_checker_timeout` | duration | `5s` | 1.0.0 | yes | Bound on each Probe as a whole (connect, send, answer); a slower answer is a failed Probe. `0` means the default; negative is a startup error |

The [examples README](../examples/README.md#choosing-the-right-algorithm) has
a table for choosing a Balancer.

## Backend settings

Each `backends` entry is one Backend, with its own Probe, connection pool and
stats row.

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `backends` | list | required | 0.1.0 | yes | The Backends; at least one |
| `backends[].url` | string | required | 0.1.0 | yes | The Backend address, `host:port`; see [Validation rules](#validation-rules) for what is accepted |
| `backends[].health_check_path` | string | `/` | 0.1.0 | yes | Path each Probe requests with a `GET`. Redirects are not followed |
| `backends[].health_check_status` | int | any 2xx or 3xx | 1.0.0 | yes | The exact status a Probe must answer (e.g. `204`); any other status is a failed Probe. Must be 200-599; `0` means unset |
| `backends[].weight` | int | - | 0.1.0 | yes | Share of traffic under `w-round-robin`, which requires a positive weight on every Backend; other Balancers ignore it |
| `backends[].max_conn` | int | `512` | 0.1.0 | yes | Max connections divisor opens to this Backend; an open Streamed response holds one. `0` or negative means the default |
| `backends[].max_conn_timeout` | duration | `30s` | 0.1.0 | yes | How long a request waits for a free connection when all `max_conn` are busy. `0` or negative means the default |
| `backends[].max_conn_duration` | duration | `10s` | 0.1.0 | yes | A connection to the Backend is closed once it has been open this long. `0` or negative means the default |
| `backends[].max_idle_conn_duration` | duration | `10s` | 0.1.0 | yes | An idle connection to the Backend is closed after this long. `0` or negative means the default |
| `backends[].max_idemponent_call_attempts` | int | `5` | 0.1.0 | yes | Attempts, the first included, for an idempotent request whose connection fails. The key is spelled `idemponent`. `0` or negative means the default |

A Reload compares entries as a whole: an entry left as it was keeps its
Backend, while a changed entry is a new Backend.

## Monitoring settings

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `monitoring.host` | string | `localhost` | 0.1.0 | no | Address the Monitoring server listens on. Set `0.0.0.0` when an orchestrator probes `/healthz` and `/ready` from outside the host |
| `monitoring.port` | string | `8001` | 0.1.0 | no | Port the Monitoring server listens on |

What the Monitoring server serves is in [Observability](observability.md).

## Logging settings

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `logging.format` | string | `json` | 1.0.0 | no | Application log encoding on stderr: `json` or `console` |
| `logging.level` | string | `info` | 1.0.0 | no | Minimum Application log level: `debug`, `info`, `warn`, `error`, `dpanic`, `panic` or `fatal` |
| `logging.access_log` | bool | `false` | 1.0.0 | no | Write the Access log, one JSON line per answered request, to stdout |

There is no file logging: Application logs go to stderr and the Access log to
stdout. The field reference is in [Observability](observability.md).

## Server settings

Every `server` key needs a restart to change. Divisor serves HTTP/1.1 with
fasthttp and HTTP/2 with Go's `net/http`; a key marked *HTTP/1.1 path only* has
no effect under `http_version: http2`.

### Protocol and TLS

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `server.http_version` | string | `http1` | 0.3.0 | no | `http1` (HTTP/1.1; `http1.1` is accepted too) or `http2`; any other value is a startup error. `http2` requires `cert_file` and `key_file`, and also answers HTTP/1.1 clients over TLS |
| `server.cert_file` | string | - | 0.3.0 | no | TLS certificate file. Divisor serves TLS when both `cert_file` and `key_file` are set; a missing file, or a pair that does not load, is a startup error |
| `server.key_file` | string | - | 0.3.0 | no | TLS private key file for `cert_file` |
| `server.tls_min_version` | string | runtime default | 1.0.0 | no | Minimum accepted TLS version: `"1.2"` or `"1.3"` (quote it); any other value is a startup error. Only matters when TLS is served |
| `server.http2.max_concurrent_streams` | int | `250` | 1.0.0 | no | Max concurrent streams a client may open per HTTP/2 connection. HTTP/2 path only. `0` or negative means the default; above `2147483647` is a startup error |

### Connections

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `server.concurrency` | int | `262144` | 0.3.0 | no | Max client connections served at once. HTTP/1.1 path only. `0` or negative means the default |
| `server.max_idle_worker_duration` | duration | `10s` | 0.3.0 | no | How long an idle worker of the connection worker pool is kept. HTTP/1.1 path only. `0` or negative means the default |
| `server.tcp_keepalive_period` | duration | Go's `15s` | 0.3.0 | no | TCP keep-alive period for accepted client connections, on both paths. `0` or negative keeps Go's default |
| `server.disable_keepalive` | bool | `false` | 0.3.0 | no | Close each client connection after its first response |
| `server.max_conns_per_ip` | int | unlimited | 1.0.0 | no | Max concurrent connections per Client IP. HTTP/1.1 path only. `0` or negative means unlimited |
| `server.max_requests_per_conn` | int | unlimited | 1.0.0 | no | Max requests served on one keep-alive connection before it is closed. HTTP/1.1 path only. `0` or negative means unlimited |
| `server.read_buffer_size` | int | `4096` | 1.0.0 | no | Per-connection read buffer in bytes; it bounds the request header size, so raise it for large cookies or JWTs. HTTP/1.1 path only. `0` or negative means the default |
| `server.write_buffer_size` | int | `4096` | 1.0.0 | no | Per-connection write buffer in bytes. HTTP/1.1 path only. `0` or negative means the default |

### Timeouts and limits

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `server.read_timeout` | duration | unlimited | 0.3.0 | no | Time allowed to read a whole request, body included. `0` means unlimited |
| `server.write_timeout` | duration | unlimited | 0.3.0 | no | Time allowed to write a response. A non-zero value also caps a whole [Streamed response](#streamed-responses). `0` means unlimited |
| `server.idle_timeout` | duration | `read_timeout` | 0.3.0 | no | How long a keep-alive connection waits for its next request, idle HTTP/2 connections included. `0` means the value of `read_timeout` (unlimited when that is unset) |
| `server.proxy_timeout` | duration | `60s` | 1.0.0 | no | Bound on each attempt at a Backend (dial and full round trip); expiry answers `504`. For a Streamed response it bounds the time to the status and headers. `0` or negative means the default: there is no unlimited setting, so a hung Backend cannot hold a connection forever |
| `server.max_request_body_size` | int | `4194304` (4 MiB) | 1.0.0 | no | Max request body in bytes; a larger body is answered `413` and never reaches a Backend. `0` or negative means the default |
| `server.graceful_shutdown_timeout` | duration | `30s` | 1.0.0 | no | Budget for draining in-flight requests on `SIGINT`/`SIGTERM`; hitting it makes divisor exit with status 1. `0` means the default; negative is a startup error |
| `server.dns_cache_duration` | duration | `60s` | 1.0.0 | no | How long a resolved Backend IP is reused, by the proxy and by Probes, before resolving again. Set a few seconds when Backend IPs churn, e.g. Kubernetes pods. `0` means the default; negative is a startup error |

### Streaming

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `server.streaming` | bool | `false` | 1.0.0 | no | Forward a Backend response as its bytes arrive when it is `text/event-stream` or has no `Content-Length`; every other response is buffered. See [Streamed responses](#streamed-responses) |
| `server.stream_idle_timeout` | duration | `5m` | 1.0.0 | no | Ends a Streamed response after this long with no data from the Backend, or with a write to the client blocked (a client that stopped reading). `0` means the default; negative is a startup error; there is no unlimited setting |

Header names are always normalized to canonical form (`x-api-key` →
`X-Api-Key`) on both the request and the response, as RFC 9110 §5.1 makes them
case-insensitive; Middleware lookups such as
`ctx.Request.Header.Peek("X-Api-Key")` therefore match whatever case the
client sent.

## Custom headers

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `custom_headers` | map | - | 0.1.0 | no | Headers set on every request sent to a Backend: header name to one special variable. A custom header replaces a same-named header from the client |

**Special variables**, the only values accepted (anything else is a startup
error):

- `$remote_addr`: the Client IP, the TCP peer that connected to divisor
- `$time`: the request time, always UTC in RFC 3339 with milliseconds, e.g.
  `2026-08-23T09:41:07.123Z`
- `$uuid`: a new UUID per request
- `$incremental`: the Request sequence number, unique and increasing per
  Backend for the life of the process

Divisor always forwards `X-Forwarded-For`: the Client IP is appended to any
chain the client sent (`client-sent, peer-ip`), so a Backend reads the
rightmost entry as the address divisor actually saw. Client-sent chains are
passed through, never trusted: behind another proxy, the Client IP (and the
ip-hash key) is that proxy. A custom header named `X-Forwarded-For` replaces
that chain.

[`production.config.yaml`](../examples/production.config.yaml) sets `$uuid`, `$time` and
`$incremental`, and leaves `X-Forwarded-For` to divisor.

## Middlewares

| Key | Type | Default | Since | Reload | Description |
| --- | --- | --- | --- | --- | --- |
| `middlewares` | list | - | 0.6.0 | no | The Middlewares, run in list order on the request and in reverse order on the response |
| `middlewares[].name` | string | required | 0.6.0 | no | Identifies the Middleware in logs; required even when `disabled` |
| `middlewares[].disabled` | bool | `false` | 0.6.0 | no | Skip this entry; a disabled entry needs neither `code` nor `file` |
| `middlewares[].code` | string | - | 0.6.0 | no | Inline Go source; set exactly one of `code` and `file` |
| `middlewares[].file` | string | - | 0.6.0 | no | Path to a Go source file; set exactly one of `code` and `file` |
| `middlewares[].config` | map | - | 0.6.0 | no | Free-form values handed to the Middleware's `New`; the only place unknown keys are allowed |

How to write one is in [Middleware](middleware.md);
[`middleware.config.yaml`](../examples/middleware.config.yaml) declares
several.

## Validation rules

Divisor validates the whole file at startup and exits non-zero on the first
error, naming it. A Reload applies the same rules and is rejected on any error.

- **Unknown keys are errors**: a misspelled or removed key anywhere in the
  config (`proxy_timout`, `disable_header_names_normalizing`, ...) fails
  startup with the offending key and line, instead of being silently ignored.
  `middlewares[].config` stays free-form
- **Backend address**: `backends[].url` must be a dialable `host:port`. An
  optional `http://` scheme and a bare trailing slash are accepted and
  stripped, and a missing port defaults to `80`. A path, query, or userinfo is
  rejected at startup, and so is `https://`: divisor does TLS termination
  itself and always speaks plain HTTP to Backends
- **HTTP/2 requires TLS**: `server.http_version: http2` requires both
  `cert_file` and `key_file`; there is no cleartext HTTP/2
- **Weighted round-robin**: every Backend needs a positive `weight`, except
  that a single Backend runs as `round-robin`
- **Middlewares**: each needs a `name`, and either `code` or `file` (not both)
  unless `disabled: true`
- **Custom headers**: values accept only the four special variables
- **Negative durations**: `health_checker_timeout`,
  `server.graceful_shutdown_timeout`, `server.dns_cache_duration` and
  `server.stream_idle_timeout` reject a negative value instead of falling back
  to the default

## Reload

Divisor applies an edited config file without a restart when it receives
`SIGHUP` or notices the file change (a Kubernetes ConfigMap update included).
Only the keys marked `yes` in the Reload column may change: `backends`,
`health_checker_time` and `health_checker_timeout`. An edit touching any other
key, or an invalid new config, is rejected as a whole; divisor keeps serving
the running config and logs why, naming every restart-only key that changed.
Writing out a default explicitly is no change. A Backend entry left as it was
keeps its liveness and stats, even if it moved in the list. A rejected edit
changes nothing rather than applying only its reloadable part, so divisor never
runs a mix of the old and the new file.

## Streamed responses

With `server.streaming: true`, a Backend response whose `Content-Type` is
`text/event-stream` or that has no `Content-Length` (a chunked body) is
forwarded as its bytes arrive, on HTTP/1.1 and HTTP/2 alike. Every other
response is buffered exactly as with streaming off. A Streamed response:

- must deliver its status and headers within `server.proxy_timeout` (`504`
  otherwise), and ends once the Backend sends nothing, or the client takes
  nothing, for `server.stream_idle_timeout`; while data flows it has no total
  time limit
- is cut by a non-zero `server.write_timeout`, which bounds the whole response
  on both stacks: leave it unset, or set it above your longest stream
- is aborted, Backend connection included, the moment its client disconnects
  (an HTTP/1.1 client half-closing its connection counts as disconnecting)
- never looks complete to the client when it ends any way but the Backend
  finishing it (idle timeout, Backend failure, write timeout): HTTP/2 resets
  the stream, HTTP/1.1 closes the connection without the final chunk
- holds one Backend connection while open, so `backends[].max_conn` caps
  concurrent streams per Backend
- drops the Backend's trailers
- keeps graceful shutdown waiting: a stream still open at
  `server.graceful_shutdown_timeout` makes divisor exit with status 1

On HTTP/1.1, a request pipelined behind an open stream is not served: the
connection closes after the stream and the client must resend it. What a
Middleware sees of a Streamed response is in
[Middleware](middleware.md#streamed-responses).

## Buffering limits

- Response streaming is opt-in. With `server.streaming` off (the default),
  the whole Backend response is read before it is sent to the client, so
  Server-Sent Events and other never-ending bodies end with `504` when
  `server.proxy_timeout` expires. Long-polling (a response that does
  eventually end) works either way; set `proxy_timeout` above the poll time.
- Request bodies are never streamed: an upload is buffered whole, up to
  `server.max_request_body_size`, before it is sent to the Backend.
- A client that disconnects does not cancel a buffered Backend request; the
  attempt runs until the Backend answers or `server.proxy_timeout` expires,
  and the result is dropped. A Streamed response is aborted at once.
