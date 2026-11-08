## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. These numbers come from one short smoke round (3s), not a measurement. [How this is measured](https://github.com/aaydin-tr/divisor/blob/a43e547b3b1ff32bb8fd680a3423a43118f6f0ff/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 | peak memory |
| --- | --- | ---: | ---: | ---: | ---: |
| divisor | feature/response-batching@a43e547-dirty | 30,553 | 1.87 ms | 3.75 ms | 8.8 MB |
| nginx | 1.31.8 | 25,539 | 2.40 ms | 3.91 ms | 5.2 MB |
| Traefik | v3.7.17 | 8,412 | 6.81 ms | 18.31 ms | 41.2 MB |
| Caddy | 2.11.7 | 7,246 | 7.89 ms | 17.77 ms | 21.3 MB |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| nginx | 1.31.8 | 0.30 ms | 1.95 ms | 5.09 ms |
| divisor | feature/response-batching@a43e547-dirty | 0.34 ms | 0.90 ms | 4.12 ms |
| Traefik | v3.7.17 | 0.77 ms | 9.70 ms | 16.51 ms |
| Caddy | 2.11.7 | 1.02 ms | 24.29 ms | 33.37 ms |
