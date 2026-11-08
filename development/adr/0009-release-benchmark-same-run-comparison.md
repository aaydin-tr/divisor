# The Release benchmark compares within one run, one core per Contender, and publishes only on request

Every stable release runs a Release benchmark (CONTEXT.md) on a GitHub-hosted `ubuntu-latest` runner: the divisor being released, Caddy, nginx and Traefik, each in the Equivalent setup, serve the same Workloads one after another, three interleaved rounds each, and the median is reported. Shared runners change CPU model and neighbors between runs, so numbers from two runs are never compared: only rows from the same run are.

Each Contender is pinned to one core and 512 MB. On a 4-vCPU runner the load generator needs two cores and the Benchmark backends one so that neither limits a Contender, and one core makes "one worker per core" the same setting for every proxy, so the numbers read per core.

The benchmark runs on every stable tag, but its numbers reach the release notes only when the annotated tag message carries the line `benchmark: publish` (`make tag VERSION=… BENCHMARK=publish`) or a manual run asks for it. A run with any failed row never publishes. Unpublished numbers stay visible in the public workflow run summary: "publish" means "on the release page", not "visible".

## Considered Options

- **Compare against numbers stored from earlier releases** — rejected: the difference would mostly measure runner drift.
- **A self-hosted dedicated machine** — rejected: stable numbers, but a machine to maintain, and GitHub advises against self-hosted runners on public repositories.
- **Let each Contender use every core** — rejected: on four vCPUs the load generator becomes the bottleneck and the table measures it instead.
- **Publish every release automatically** — rejected: whether a release shows its numbers is the maintainer's call per release.
- **A required-reviewer environment gating the publish** — rejected: declining leaves a failed or pending job on every unpublished release.
- **A repository variable as the switch** — rejected: a standing switch that must be remembered and turned back off; the tag message belongs to the one release it decides.
- **Pull `latest` Contender images** — rejected: the comparison would shift whenever another project ships. Versions are pinned and bumped by a monthly Dependabot PR.

## Consequences

- Absolute numbers on two release pages are not comparable; only rows on one page are.
- Changing the method (cores, Workloads, rates, connection count) makes later release pages incomparable with earlier ones, so the release that changes it says so.
- Only amd64 is measured, although the release image is multi-arch.

## Amended 2026-11-06 and 2026-11-08

- **No Baseline release.** A run no longer measures the previous stable divisor release next to the divisor being released, and the section no longer opens with divisor's change against it (Throughput req/s and Fixed-rate latency p99, each judged an improvement, a regression or within noise). The maintainer does not want a percentage gain against the previous release on every release page. Rows still compare only within one run.
- **Caddy replaces HAProxy** as a Contender, in the Equivalent setup like the others.
- **Ranked tables.** Throughput rows are ranked by requests per second, Fixed-rate latency rows by p50, a failed row last; this replaces the fixed row order (divisor first, then the others alphabetically).
- **Peak memory.** The Throughput table adds each Contender's peak memory, the cgroup v2 `memory.peak` of its container read after Throughput, n/a when unreadable. So that the page cache of a Contender's own files does not count in round 1, each Contender is started once, unmeasured, before round 1.
