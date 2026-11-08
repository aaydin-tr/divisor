# Response batching is on by default up to GOMAXPROCS 2, because it also helps independent clients

With `server.disable_response_batching: false` (the default), the HTTP/1.1 handler calls `runtime.Gosched()` after it serves a response that is not a **Streamed response** (CONTEXT.md) and before fasthttp writes it (**Response batching**). The config forces the key to `true` when `GOMAXPROCS` at startup is above 2 (`disableResponseBatchingAboveTwoCPUs`), so the yield never runs on 3 CPUs or more. Go sets `GOMAXPROCS` from the CPU affinity and the cgroup CPU quota (a quota under 2 is rounded up to 2), so a `docker run --cpuset-cpus` of one or two CPUs, a Kubernetes `limits.cpu` of 2 or less, or `GOMAXPROCS=1` all batch. fasthttp writes each response once, after the handler returns; the yield changes when that write happens, not how many there are. Since 1.1.0.

## Evidence

Every number is a 20 s oha 1.16.0 run against the same divisor build, 4 Benchmark backends on their own cores, divisor pinned to its own core(s), at least two repeats; "on" and "off" are the two values of the key. The ranges span the repeats.

**One CPU, `GOMAXPROCS=1`, closed loop (each client sends as fast as it can), 2026-11-08/09, Docker Desktop VM:**

| clients x connections | req/s off | req/s on | p50 ms off → on | p99 ms off → on |
|---|---|---|---|---|
| 1 x 64 (one load generator holding every connection) | 27,900–28,000 | 31,800–33,300 | 2.13 → 1.95–1.99 | 4.48 → 3.02–4.01 |
| 8 x 8 | 16,500–23,000 | 30,800–31,700 | 2.59–3.38 → 1.93–1.98 | 5.45–9.90 → 3.33–3.95 |
| 64 x 1 (independent single-connection clients) | 16,800–19,800 | 24,200–29,100 | 2.91–3.18 → 2.18–2.51 | 7.25–10.22 → 3.97–5.01 |
| 1 x 1 (one client, one connection) | 4,590–4,620 | 4,550–4,600 | 0.20 → 0.20–0.21 | 0.49–0.50 → 0.46–0.48 |

**One CPU, open loop at 12,000 req/s in total (below saturation for both modes):** p50 and p99 equal or lower with batching on, for one client holding 64 connections (p99 1.66–6.20 ms off, 1.20–1.23 on) and for 64 independent clients (p99 1.71–7.13 ms off, 1.41–1.50 on).

**Two CPUs (2026-11-08, three rounds each):** a two-core cpuset gave +18.6% req/s, p50 -16%, p99.9 +5.4% with overlapping ranges; a 2-CPU quota gave +24.2% req/s, p99 -22%, p99.9 -21%. The Fixed-rate latency Workload at 5,000 req/s was unchanged in both.

**Four CPUs (2026-11-05):** -6.7% req/s, p50 +9.5%, Fixed-rate p99.9 +26%, with no overlap: with more than one P the yield only delays the write.

Divisor's own voluntary and involuntary context switches, summed over its threads, are the same on and off at saturation, so the saving is not in divisor's scheduling against the kernel. The exact mechanism is not isolated: the best reading is that the yield lets the one P finish every ready Backend answer before any client write, so the clients' next requests arrive as a group and each netpoll wake-up carries more of them. An earlier comment claimed each client is woken once per batch; the 64 x 1 rows contradict that, since each of those clients holds one connection.

## Considered Options

- **Off by default (opt-in)** — rejected: the gain holds for independent single-connection clients and for fan-in clients, with no measured cost for a lone client or below saturation, so a default that most 1- and 2-CPU deployments would want off is not supported by the evidence.
- **On at every CPU count** — rejected: measured slower at 4 CPUs.
- **On at `GOMAXPROCS == 1` only** — superseded on 2026-11-08: the two-CPU runs (cpuset and quota) gained +18–24% at no Fixed-rate cost, and Go rounds a CPU quota under 2 up to `GOMAXPROCS` 2, so a `limits.cpu: 1` pod would otherwise never batch.
- **Gate on `runtime.NumCPU()`** — rejected: it reflects affinity only and ignores a cgroup CPU quota; `runtime.GOMAXPROCS(0)` reflects both.
- **No knob** — rejected: a deployment that measures a regression needs a way out without a rebuild.

## Consequences

- The gate reads `GOMAXPROCS` once at startup, so a CPU quota raised above 2 while divisor runs keeps batching on until a restart, and a Reload compares two configs prepared against the same value.
- The Release benchmark pins each Contender to one core, so divisor's Throughput row includes this gain; the benchmark has no on-versus-off Workload, and this ADR is where the comparison is recorded.
- Pipelined responses were already coalesced by fasthttp, which skips the flush while another request is buffered; batching does not change that.
- The yield runs before fasthttp arms the per-response write deadline, so its delay never counts against `server.write_timeout`.
- The HTTP/2 path (net/http) does not batch.
