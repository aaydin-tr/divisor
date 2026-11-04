## Performance

**Against the Baseline release v1.0.0:** Throughput req/s not compared: a smoke run's 3s numbers only check the Equivalent setup. Fixed-rate latency p99 not compared: a smoke run's 3s numbers only check the Equivalent setup.

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. These numbers come from one short smoke round (3s), not a measurement. [How this is measured](https://github.com/aaydin-tr/divisor/blob/aa36fa6b18a318a475ea591120ef99e0a50eeae8/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | aa36fa6-dirty | 22,416 | 2.58 ms | 5.52 ms |
| divisor (Baseline) | v1.0.0 | 22,003 | 2.61 ms | 5.62 ms |
| HAProxy | 3.4.7 | 29,450 | 1.98 ms | 4.44 ms |
| nginx | 1.31.8 | 24,756 | 2.45 ms | 4.41 ms |
| Traefik | v3.7.17 | 8,122 | 6.95 ms | 17.96 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| divisor | aa36fa6-dirty | 0.34 ms | 0.81 ms | 3.59 ms |
| divisor (Baseline) | v1.0.0 | 0.39 ms | 4.64 ms | 10.82 ms |
| HAProxy | 3.4.7 | 0.39 ms | 1.82 ms | 4.30 ms |
| nginx | 1.31.8 | 0.31 ms | 2.11 ms | 5.93 ms |
| Traefik | v3.7.17 | 0.86 ms | 23.95 ms | 31.37 ms |
