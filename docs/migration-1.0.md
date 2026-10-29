# Migrating to 1.0

This page lists every change since **0.6.0**, the last 0.x release, that can
break a working setup. Each entry says what changed, who is affected, and what
to do. Work through the checklist from the top. Entries marked **startup**
stop divisor from starting until you fix them. The rest change what divisor
does once it runs.

## Checklist

Config file

- [ ] [Remove unknown and removed config keys](#unknown-config-keys-fail-startup) (**startup**)
- [ ] [Fix Backend URLs with `https://`, a path, a query or userinfo](#backend-urls-are-validated) (**startup**)
- [ ] [Use `http1` or `http2` for `server.http_version`](#serverhttp_version-is-validated) (**startup**)
- [ ] [Set `host: localhost` if divisor must stay loopback-only](#the-client-listener-binds-0000-by-default)

Logs

- [ ] [Read logs from stderr instead of the log file](#file-logging-is-gone)
- [ ] [Expect JSON lines, or set `logging.format: console`](#application-logs-are-json-by-default)

Traffic

- [ ] [Raise `server.proxy_timeout` for Backends slower than 60s](#backend-requests-time-out-after-60s)
- [ ] [Raise `server.max_request_body_size` for uploads over 4MB](#request-bodies-are-capped-at-4mb-on-both-stacks)
- [ ] [Update clients and alerts that expect 500 for a failed proxy](#error-status-codes-changed)
- [ ] [Check what your Backends read from the request headers](#what-backends-receive-changed)

Health checks

- [ ] [Set `health_check_status: 200` to keep the old Probe rule](#probes-accept-any-2xx-or-3xx)
- [ ] [Alert on dead Backends instead of relying on divisor exiting](#divisor-starts-and-stays-up-with-no-alive-backend)

Process

- [ ] [Expect startup failures to exit 1](#startup-failures-exit-non-zero)
- [ ] [Stop divisor with SIGTERM, not SIGHUP](#sighup-reloads-instead-of-terminating)
- [ ] [Stage config edits outside the live file](#editing-the-config-file-reloads-divisor)

Middleware

- [ ] [Return `middleware.ErrShortCircuit` to answer a request yourself](#a-middleware-short-circuits-with-errshortcircuit)
- [ ] [Check `OnResponse` ordering between Middlewares](#onresponse-runs-in-reverse-config-order)

Balancing and monitoring

- [ ] [Expect each ip-hash client to move once](#ip-hash-clients-move-to-a-new-backend-once)
- [ ] [Re-key dashboards built on `backend_hash`](#stats-backend_hash-and-avg_res_time-changed-meaning)

Building

- [ ] [Build from source with Go 1.27](#building-from-source-needs-go-127)

To test a config before you switch, run the 1.0 binary against a copy of it.
In the copy, change `port` and `monitoring.port` to free ports, so the test
instance does not take live traffic: divisor binds the client port with
`SO_REUSEPORT` and would share it with the running instance.

```bash
divisor --config /absolute/path/to/config.copy.yaml
```

Every config error makes divisor exit 1 before it binds anything. The log
names the problem. If divisor starts instead, stop it with Ctrl-C.

## Config file

### Unknown config keys fail startup

**What changed:** 0.x silently ignored any key it did not know. 1.0 decodes
the config strictly: a misspelled or removed key anywhere fails startup and
the error names the key and its line. The free-form `middlewares[].config` map
is the only exception. One key was removed:
`server.disable_header_names_normalizing`. Header names are now always
normalized, on both the request and the response.

```text
parsing config file /etc/divisor/config.yaml: yaml: unmarshal errors:
  line 33: field disable_header_names_normalizing not found in type config.Server
```

**Who is affected:** every config with a typo in a key, which 0.x silently
ignored, and every config that sets `disable_header_names_normalizing`. 0.6.0's
`examples/advanced.config.yaml` sets it.

**What to do:** fix or delete each key the error names, then run divisor again
until it starts. Delete the removed key:

```diff
 server:
-  disable_header_names_normalizing: true
```

If you had set it to `true`, Backends now receive canonical header names
(`X-Api-Key`), whatever case the client used. Middleware lookups such as
`ctx.Request.Header.Peek("x-api-key")` now match any case. See
[Configuration](configuration.md#validation-rules).

### Backend URLs are validated

**What changed:** 0.x stripped `http://` and `https://` from `backends[].url`
and used what was left as the dial address. 1.0 turns the url into a
Backend address (`host:port`) and rejects anything it cannot honor at startup:

| `url` | 0.6.0 | 1.0 |
| --- | --- | --- |
| `https://api:443` | Stripped. Traffic went to the Backend as **plaintext** | Startup error: divisor speaks only plain HTTP to Backends |
| `api:8080/v1`, `api:8080?x=1` | Dial address `api:8080/v1`, so every request to that Backend failed | Startup error |
| `http://user:pass@api:8080` | Undialable address, so every request failed | Startup error |
| `http://api:8080/` | Undialable address, so every request failed | Accepted as `api:8080` |
| `api` | Port 80 | Port 80 (unchanged) |

**Who is affected:** configs that list an `https://` Backend, or a url with a
path, query or credentials.

**What to do:** point each Backend at a port that speaks plain HTTP:

```diff
 backends:
-  - url: https://api.internal:443
+  - url: http://api.internal:8080
```

Divisor never speaks TLS to a Backend. If the Backend only accepts TLS, run a
plaintext hop in front of it, such as a sidecar or a local reverse proxy, and
point divisor at that hop.

### `server.http_version` is validated

**What changed:** 0.x served HTTP/1.1 for any `http_version` other than
exactly `http2`, so `HTTP2` or `h2` silently gave you HTTP/1.1. 1.0 accepts
only `http1` (or `http1.1`) and `http2`. Leaving the key unset still means
`http1`. Any other value fails startup:
`server.http_version must be http1 or http2, got "h2"`.

**Who is affected:** configs with any other spelling.

**What to do:** set `http1` or `http2`. If you meant `http2`, the listener now
really serves HTTP/2, which requires `cert_file` and `key_file`.

### The client listener binds 0.0.0.0 by default

**What changed:** the default for the top-level `host` changed from
`localhost` to `0.0.0.0`, so divisor now accepts connections on every
interface. `monitoring.host` keeps its `localhost` default.

**Who is affected:** configs without a `host` key that relied on divisor being
reachable only from the same machine.

**What to do:** to keep the old binding, set it explicitly:

```yaml
host: localhost
```

## Logs

### File logging is gone

**What changed:** 0.x wrote every Application log line both to stdout and to
a file: `/var/log/divisor/divisor.log` on Linux and macOS,
`%LocalAppData%\divisor\divisor.log` on Windows, or `./divisor.log` when that
directory could not be created. 1.0 writes no files. Application logs go to
**stderr** only. Stdout carries the Access log and nothing else, and the
Access log is off by default (`logging.access_log`).

**Who is affected:** anything that reads the log file (logrotate, a log
shipper tailing it), and anything that captures divisor's stdout to get its
logs.

**What to do:** capture stderr instead.

- systemd: nothing to do. journald captures stderr (`journalctl -u divisor`).
- Docker and Kubernetes: nothing to do. `docker logs` and `kubectl logs`
  show stderr.
- Shell: redirect stderr to the file your tooling reads:

  ```bash
  divisor --config /etc/divisor/config.yaml 2>>/var/log/divisor/divisor.log
  ```

If you turn on the Access log, keep stdout separate from stderr. Each stream
then carries one kind of line. See [Observability](observability.md).

### Application logs are JSON by default

**What changed:** 0.x logged human-readable console lines with keys `T`, `L`,
`C` and `M`. 1.0 logs JSON lines with keys `time`, `level` and `msg`, and
drops the caller. The new `logging` section configures the format and level:

```json
{"level":"info","time":"2026-10-29T09:41:07.123Z","msg":"Config file parsed successfully"}
```

**Who is affected:** people and parsers that read the console format.

**What to do:** point log parsers at the JSON fields. To keep readable lines,
set:

```yaml
logging:
  format: console
```

## Traffic

### Backend requests time out after 60s

**What changed:** 0.x waited for a Backend's response forever. 1.0 bounds
each request to a Backend with `server.proxy_timeout`, which defaults to `60s`.
When it expires the client gets `504`. `0` means the default, and there is no
unlimited setting, so a hung Backend cannot hold a client connection forever.

**Who is affected:** Backends that legitimately take longer than 60s, such as
long-polling, slow reports or large exports. Also Server-Sent Events and other
endless responses: they never worked through 0.x, which buffered them, and
with the default settings they now end in a `504` at 60s.

**What to do:** raise the bound for slow Backends:

```yaml
server:
  proxy_timeout: 5m
```

For event streams, turn on Streamed responses (`server.streaming: true`). Do
not raise `proxy_timeout` for them, because a bigger timeout does not make a
response stream. See
[Configuration](configuration.md#streamed-responses).

### Request bodies are capped at 4MB on both stacks

**What changed:** with `http_version: http1`, 0.x already rejected bodies over
4MB, but with `400`. With `http2` it had no cap. 1.0 caps both stacks at
`server.max_request_body_size` (default 4MB) and answers `413` before the
body reaches a Backend.

**Who is affected:** HTTP/2 deployments that accept uploads over 4MB, and
clients that treat the old `400` as "too large".

**What to do:** raise the cap. The value is in bytes:

```yaml
server:
  max_request_body_size: 52428800 # 50MB
```

### Error status codes changed

**What changed:** divisor's own error answers:

| Situation | 0.6.0 | 1.0 |
| --- | --- | --- |
| Backend unreachable (refused, reset, dial timeout) | `500`, body carries the dial error | `502 {"message":"bad gateway"}` |
| Backend still silent at `proxy_timeout` | waited forever | `504 {"message":"gateway timeout"}` |
| No Alive Backend | the process exited (see [below](#divisor-starts-and-stays-up-with-no-alive-backend)) | `503 {"message":"no backends available"}` |
| Request body over the cap | `400` on HTTP/1.1, no cap on HTTP/2 | `413 {"message":"request body too large"}` |
| A Middleware returns an error other than `ErrShortCircuit` | the response as the Middleware left it | `500 {"message":"internal server error"}`, the error logged |
| A Middleware panics | the process crashed | `500 {"message":"internal server error"}`, panic and stack logged |

**Who is affected:** clients, retry policies, dashboards and alerts that match
on `500` for a failed proxy, or that parse the dial error out of the body.

**What to do:** match on `502` and `504` instead. To find the cause of a
`502`, look for the Application log line `error when proxying the request`.
The body no longer carries it.

### What Backends receive changed

Each of these changes what a Backend sees in the forwarded request:

- **`X-Forwarded-For` is appended to, not replaced.** 0.x overwrote any
  client-sent value with the Client IP. 1.0 appends the Client IP to the
  client's chain (`client-sent, 203.0.113.7`). A Backend that trusts the
  **leftmost** entry now trusts whatever the client wrote there. Read the
  rightmost entry, or send the Client IP in a header of its own:

  ```yaml
  custom_headers:
    x-real-ip: $remote_addr
  ```

- **`$time` is UTC.** 0.x stamped local time followed by a literal `Z`, which
  was wrong by the host's UTC offset. 1.0 sends real UTC in RFC 3339 with
  milliseconds (`2026-08-23T09:41:07.123Z`). If a Backend corrected for the
  offset, remove that correction.
- **No invented headers.** If the client sent no `User-Agent`, 0.x added
  `User-Agent: fasthttp`. If a request other than `GET` or `HEAD` had no
  `Content-Type`, 0.x added `Content-Type: text/plain; charset=utf-8`. 1.0
  forwards headers as the client sent them. A Backend that required either header now has to get it
  from the client, or from a Middleware that sets it.
- **Hop-by-hop headers named in `Connection` are removed** in both
  directions, as RFC 9110 requires. A Backend that read a header the client
  listed in `Connection` no longer receives it.
- **Header names are canonical.** This only changes anything if you had set
  `disable_header_names_normalizing: true`
  ([above](#unknown-config-keys-fail-startup)).

## Health checks

### Probes accept any 2xx or 3xx

**What changed:** in 0.x a Probe succeeded only on exactly `200`. In 1.0 any
`2xx` or `3xx` status makes a Backend Alive. Redirects are judged as they are
and never followed. One deadline, `health_checker_timeout` (default `5s`),
covers the whole Probe.

**Who is affected:** Backends whose health path answers `204`, `301` or
`302`. 0.x kept them Down forever, so they never got traffic. 1.0 sends them
traffic. Also Backends that report "not ready" with a `3xx`.

**What to do:** to keep the old rule for a Backend, set the exact status its
Probe must return:

```yaml
backends:
  - url: http://10.0.0.5:8080
    health_check_path: /healthz
    health_check_status: 200
```

### Divisor starts and stays up with no Alive Backend

**What changed:** in 0.x:

- A Backend that was Down at startup never got traffic, even after it
  recovered.
- If no Backend was Alive at startup, divisor logged `No available servers`
  and exited.
- When the last Alive Backend went Down, divisor panicked and exited.

In 1.0, every Backend Rejoins as soon as a Probe succeeds. Divisor starts
with zero Alive Backends, answers `503` while none is Alive, and keeps
running.

**Who is affected:** deployments that relied on divisor exiting to notice dead
Backends or a mistyped url, and supervisors that restarted divisor to bring
Backends back.

**What to do:** alert on Backend liveness instead: `backend_alive` on
`/metrics`, `is_host_alive` on `/stats`, and the rate of `503` answers. The
new `/ready` endpoint on the Monitoring server does not reflect Backend
health, by design. See [Observability](observability.md).

## Process

### Startup failures exit non-zero

**What changed:** in 0.x, most startup failures logged an error and exited
with status `0`. These were a missing or invalid config, a Middleware that
does not compile, and a client port that cannot be bound. Some failures did
not exit at all. A listener that failed to serve, for example over an
unloadable TLS key pair, left the process running without serving. A
monitoring port that could not be bound was logged, and divisor carried on
without its Monitoring server. In 1.0 every one of these failures exits `1`.
That includes the monitoring bind and the TLS key pair, which divisor now
checks at startup.

**Who is affected:** scripts that check divisor's exit status, and
supervisors. systemd `Restart=on-failure` and Docker restart policies now
restart divisor after a config error, which shows up as a restart loop.

**What to do:** treat the restart loop as the signal and fix what the last log
line names. If another process holds `monitoring.port` (default `8001`), free
that port or move divisor's Monitoring server to another one.

### SIGHUP reloads instead of terminating

**What changed:** 0.x did not handle `SIGHUP`, so the signal's default action
ended the process. In 1.0, `SIGHUP` triggers a Reload: divisor re-reads its
config file and keeps running.

**Who is affected:** scripts and process managers that stop divisor with
`kill -HUP`. Also divisor started in the foreground of a terminal: closing
the terminal no longer stops it.

**What to do:** stop divisor with `SIGTERM` or `SIGINT`, which shut it down
gracefully within `server.graceful_shutdown_timeout`.

### Editing the config file reloads divisor

**What changed:** 0.x read its config once. 1.0 watches the config file, and
about one second after an edit it applies the edit. A change limited to
`backends`, `health_checker_time` and `health_checker_timeout` is applied.
Any other change is rejected as a whole and logged as
`Reload rejected, divisor keeps serving the running config`. A rejected edit
still takes effect at the next restart. See
[Configuration](configuration.md#reload).

**Who is affected:** anyone who edits the live config file ahead of a
planned restart.

**What to do:** stage the edit in another file, and move it over the config
file when you mean to apply it.

## Middleware

### A Middleware short-circuits with ErrShortCircuit

**What changed:** in 0.x, a Middleware that returned **any** error had its
response sent as it left it. That was the only way to answer a request
yourself. Returning `nil` from `OnResponse` after a Backend failure let
divisor overwrite your response with its `500`. In 1.0, only the sentinel
`middleware.ErrShortCircuit` makes a Short-circuit: divisor sends
`ctx.Response` as you left it and runs no later Middleware. Any other error
means the Middleware failed: divisor discards the response and answers `500`.
Returning `nil` never stops the chain.

**Who is affected:** every Middleware that writes its own response and then
returns an error, such as a `401`, a `403` or a fallback page. In 1.0 those
requests get a `500`.

**What to do:** return the sentinel, from either hook:

```diff
 func (g *Guard) OnRequest(ctx *middleware.Context) error {
     if len(ctx.Request.Header.Peek("X-Api-Key")) == 0 {
         ctx.SetStatusCode(401)
-        return errors.New("missing api key")
+        return middleware.ErrShortCircuit
     }
     return nil
 }
```

You can wrap the sentinel to keep a reason in the debug log:
`fmt.Errorf("missing api key: %w", middleware.ErrShortCircuit)`. The
sentinel only exists in 1.0. Divisor's interpreter always provides it, but
your editor and `go build` need the 1.0 module:
`go get github.com/aaydin-tr/divisor/middleware@v1`. See
[Middleware](middleware.md#short-circuiting).

### OnResponse runs in reverse config order

**What changed:** 0.x ran `OnResponse` hooks in config order. 1.0 runs them
in reverse config order: the Middleware that sees the request first sees the
response last. `OnRequest` order is unchanged.

**Who is affected:** configs with two or more Middlewares whose `OnResponse`
hooks depend on each other. For example, one sets a header that another reads,
or both rewrite the same response.

**What to do:** reorder the `middlewares` entries so each `OnResponse` runs
where you need it.

## Balancing and monitoring

### ip-hash clients move to a new Backend once

**What changed:** the ip-hash ring now gives every Backend 100 Virtual nodes,
placed by its Index. 0.x gave each Backend the square of the Backend count,
with keys that collided when the same address appeared twice. As a result,
most Client IPs map to a different Backend after the upgrade. The mapping is
stable after that, including across Reloads.

**Who is affected:** ip-hash setups whose Backends keep per-client state in
memory, such as sessions or warm caches.

**What to do:** plan the upgrade as you would plan a change to the Backend
set. Keep sessions in a shared store, or accept one re-login or cache miss per
client.

### /stats backend_hash and avg_res_time changed meaning

**What changed:** in `/stats`:

- `backend_hash` was a CRC32 hash of the address and config position. It is
  now the Backend's Index: its config position at startup (`0`, `1`, …), and
  a never-used number for a Backend a Reload adds.
- `avg_res_time` and the `backend_average_response_time` metric were whole
  milliseconds averaged over all requests, failed ones included. They are now
  fractional milliseconds averaged over successful requests only.

Prometheus metric names and the `address` label are unchanged. Every
`backend_*` series gains an `index` label carrying the same Index, so two
Backends with the same address no longer share one series.

**Who is affected:** dashboards and scripts that store `backend_hash` values or
compare response times across the upgrade, and recording rules or alerts that
match a `backend_*` series's full label set.

**What to do:** key Backends by `addr` (or the new `backend_hash`), and start
response-time baselines over.

## Building

### Building from source needs Go 1.27

**What changed:** the module now requires Go 1.27. 0.6.0 required Go 1.25.

**Who is affected:** `go build` and `go install` users on an older Go.

**What to do:** install Go 1.27. Alternatively, leave `GOTOOLCHAIN` at its
default (`auto`), and `go install github.com/aaydin-tr/divisor@latest`
downloads the toolchain itself.

## Fixed behavior you may notice

These changes need no action, but traffic may look different after the
upgrade:

- least-connection now picks the Backend with the fewest Pending requests.
  0.x stopped at the first Backend with fewer.
- least-response-time scores Backends by a moving average, and counts a
  failed request as slow. 0.x let a failing Backend look fast.
- `server.tcp_keepalive_period` now takes effect on both stacks. In 0.x it
  did nothing.
- `/stats` lists every Backend in config order, Down ones included.

## New in 1.0, nothing to migrate

0.x had no official container image. 1.0 publishes one to
`ghcr.io/aaydin-tr/divisor` and `aaydintr/divisor`. It runs as nonroot, has no
shell, and reads `/etc/divisor/config.yaml`. 1.0 also publishes a Helm chart
([Docker](deployment/docker.md), [Kubernetes](deployment/kubernetes.md)). If
you built your own image for 0.x, it no longer needs a `/var/log/divisor`
directory. 1.0 also adds Reload, opt-in Streamed responses, the Access log,
`/healthz` and `/ready`, and `--version`. All of them are additive.
