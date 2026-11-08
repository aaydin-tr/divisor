## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/aa36fa6c0ffee0000000000000000000000000ee/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 | peak memory |
| --- | --- | ---: | ---: | ---: | ---: |
| nginx | 1.31.8 | 25,000 | 2.50 ms | 4.50 ms | 6.0 MB |
| divisor | main@aa36fa6 | 20,000 | 3.00 ms | 5.00 ms | 12.0 MB |
| Traefik | v3.7.17 | 9,000 | 7.00 ms | 16.00 ms | 40.0 MB |
| Caddy | 2.11.7 | 7,000 | 9.00 ms | 20.00 ms | 24.0 MB |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| nginx | 1.31.8 | 0.30 ms | 0.60 ms | 4.00 ms |
| divisor | main@aa36fa6 | 0.35 ms | 0.90 ms | 5.00 ms |
| Caddy | 2.11.7 | 0.80 ms | 20.00 ms | 60.00 ms |
| Traefik | v3.7.17 | 0.90 ms | 15.00 ms | 45.00 ms |
