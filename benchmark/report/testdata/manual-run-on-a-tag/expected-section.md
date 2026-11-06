## Performance

Runner: GitHub-hosted ubuntu24 20261027.1, amd64, AMD EPYC 7763 64-Core Processor. Each proxy gets 1 core and 512 MB and balances round-robin over 4 backends, HTTP/1.1 without TLS. Each number is the median of 3 rounds. [How this is measured](https://github.com/aaydin-tr/divisor/blob/c0ffee0b18a318a475ea591120ef99e0a50eeae8/benchmark/README.md) · [Workflow run](https://github.com/aaydin-tr/divisor/actions/runs/123456789)

### Throughput

64 keep-alive connections, each sending its next request as soon as the last one is answered.

| Contender | Version | req/s | p50 | p99 |
| --- | --- | ---: | ---: | ---: |
| divisor | v1.0.0 | 23,575 | 2.54 ms | 5.27 ms |

### Fixed-rate latency

5,000 requests per second over 64 keep-alive connections, latency corrected for coordinated omission.

| Contender | Version | p50 | p99 | p99.9 |
| --- | --- | ---: | ---: | ---: |
| divisor | v1.0.0 | 0.31 ms | 0.69 ms | 6.19 ms |
