# Black-box integration suite: divisor runs as a container

The integration suite (`integration-test/`) tests divisor fully black-box: the divisor binary and its Backends each run as Docker containers (via dockertest), and tests drive real HTTP traffic from outside. We chose this over running divisor in-process inside `go test` because divisor has no library entry point (all wiring lives unexported in `package main`) and has process-global state that makes multiple in-process instances unsafe (duplicate Prometheus registration panics, health-checker goroutines without a working stop signal, `panic("All backends are down")` fired from a goroutine would kill the whole test binary). Containerizing divisor sidesteps all of that and additionally lets the suite test container-level behavior: SIGTERM graceful shutdown, real connection-refused/hang failure modes via `docker kill`/`docker pause`, and distinct client source IPs for ip-hash.

## Considered Options

- **Backends in containers, divisor in-process** — rejected: requires re-assembling `main()` from exported pieces and inherits the global-state hazards above; would need a testability refactor of `main` first.
- **No Docker, `httptest` backends** — rejected: cannot simulate container death/pause failure modes or distinct client IPs, which are the suite's core scenarios.

## Consequences

- No Go coverage data from integration tests; debugging happens via `docker logs` (zap already writes to stdout).
- ip-hash distribution tests need auxiliary client containers, since all host-originated requests share one source IP.
- The repo gains a root `Dockerfile` (multi-stage), which doubles as the distributable image.
