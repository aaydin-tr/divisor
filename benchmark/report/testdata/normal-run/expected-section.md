## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/a43e547b3b1ff32bb8fd680a3423a43118f6f0ff/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 | peak memory |
| --- | --- | ---: | ---: | ---: | ---: |
| divisor | feature/response-batching@a43e547-dirty | 30,188 | 1.96 ms | 4.34 ms | 10.7 MB |
| nginx | 1.31.8 | 24,853 | 2.44 ms | 4.37 ms | 5.3 MB |
| Traefik | v3.7.17 | 8,716 | 6.78 ms | 16.52 ms | 38.9 MB |
| Caddy | 2.11.7 | 6,810 | 8.45 ms | 20.40 ms | 22.0 MB |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| nginx | 1.31.8 | 0.30 ms | 0.62 ms | 4.10 ms |
| divisor | feature/response-batching@a43e547-dirty | 0.36 ms | 0.99 ms | 7.71 ms |
| Traefik | v3.7.17 | 0.86 ms | 16.44 ms | 52.71 ms |
| Caddy | 2.11.7 | 1.07 ms | 98.76 ms | 265.95 ms |
