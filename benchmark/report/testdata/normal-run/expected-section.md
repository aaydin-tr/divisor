## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/4ae9ae22644305c1a01c4393127980e901a10965/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | main@4ae9ae2-dirty | 31,035 | 1.90 ms | 3.72 ms |
| nginx | 1.31.8 | 25,720 | 2.40 ms | 4.05 ms |
| Traefik | v3.7.17 | 8,441 | 6.87 ms | 17.29 ms |
| Caddy | 2.11.7 | 6,965 | 8.25 ms | 19.40 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| nginx | 1.31.8 | 0.30 ms | 0.60 ms | 4.06 ms |
| divisor | main@4ae9ae2-dirty | 0.35 ms | 0.78 ms | 8.11 ms |
| Traefik | v3.7.17 | 0.77 ms | 6.41 ms | 12.53 ms |
| Caddy | 2.11.7 | 0.99 ms | 6.94 ms | 14.17 ms |
