## Performance

Runner: local machine running Docker Desktop (containerized), amd64, AMD Ryzen 5 5500. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/v1.0.0/benchmark/README.md)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | v1.0.0@23efb10 | 23,575 | 2.54 ms | 5.27 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| divisor | v1.0.0@23efb10 | 0.32 ms | 0.69 ms | 6.19 ms |
