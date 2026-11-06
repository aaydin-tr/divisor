## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. These numbers come from one short smoke round (3s), not a measurement. [How this is measured](https://github.com/aaydin-tr/divisor/blob/4ae9ae22644305c1a01c4393127980e901a10965/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | main@4ae9ae2-dirty | 29,814 | 1.92 ms | 4.30 ms |
| nginx | 1.31.8 | 26,632 | 2.33 ms | 3.47 ms |
| Traefik | v3.7.17 | 8,785 | 6.75 ms | 17.22 ms |
| Caddy | 2.11.7 | 7,200 | 7.80 ms | 18.51 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| nginx | 1.31.8 | 0.31 ms | 1.96 ms | 5.52 ms |
| divisor | main@4ae9ae2-dirty | 0.35 ms | 0.77 ms | 2.63 ms |
| Traefik | v3.7.17 | 0.84 ms | 11.15 ms | 16.47 ms |
| Caddy | 2.11.7 | 1.01 ms | 12.23 ms | 18.40 ms |
