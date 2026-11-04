# The Release benchmark compares within one run, one core per Contender, and publishes only on request

Every stable release runs a Release benchmark (CONTEXT.md) on a GitHub-hosted `ubuntu-latest` runner: the divisor being released, the Baseline release, nginx, HAProxy and Traefik, each in the Equivalent setup, serve the same Workloads one after another, three interleaved rounds each, and the median is reported. Shared runners change CPU model and neighbors between runs, so numbers from two runs are never compared: the Baseline release runs again in every run instead of being read back from its own release, and a difference smaller than 3% or the spread between rounds, whichever is larger, is reported as within noise.

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
- A release whose Baseline cannot read the benchmark config (a major config break, or `v1.0.0` against `v0.6.0`) shows no Baseline row.
- Only amd64 is measured, although the release image is multi-arch.
