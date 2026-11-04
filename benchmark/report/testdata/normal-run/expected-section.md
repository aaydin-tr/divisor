## Performance

**No Baseline release:** the checkout has no stable release tag.

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/main/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | main@aa36fa6 | 23,575 | 2.54 ms | 5.27 ms |
| HAProxy | 3.4.7 | 31,759 | 1.90 ms | 3.57 ms |
| nginx | 1.31.8 | 26,179 | 2.33 ms | 4.12 ms |
| Traefik | v3.7.17 | 8,748 | 6.70 ms | 17.11 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| divisor | main@aa36fa6 | 0.31 ms | 0.69 ms | 6.19 ms |
| HAProxy | 3.4.7 | 0.32 ms | 0.72 ms | 4.53 ms |
| nginx | 1.31.8 | 0.30 ms | 0.65 ms | 5.90 ms |
| Traefik | v3.7.17 | 0.76 ms | 8.26 ms | 17.72 ms |
