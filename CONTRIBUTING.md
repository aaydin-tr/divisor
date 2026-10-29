# Contributing to divisor

Thanks for helping. This page covers building and testing divisor, the
conventions the code and docs follow, and how a change gets merged. The user
documentation is in [`docs/`](docs/README.md); everything for contributors is
here and in [`development/`](development/).

## Prerequisites

- Go 1.27 (the `go` line in [`go.mod`](go.mod); CI uses 1.27.3)
- Docker, only for the integration suite and the release image
- A C toolchain if you want to run tests with `-race` locally (it needs cgo)

## Build and run

```bash
go build -o divisor
./divisor --config /absolute/path/to/config.yaml
```

Without `--config`, divisor reads `config.yaml` from the current directory.
`make help` lists the other targets (`fmt`, `vet`, `lint`, `test`, `check`).

## Tests

```bash
go test -race ./...
```

CI runs the unit tests with `-race`, so a data race fails the build. Run one
package or one test with `go test ./core/pool` or
`go test -run TestName ./path/to/package`.

### Integration suite

`integration-test/` is a black-box suite: it builds divisor and purpose-built
echo Backends into Docker images and drives them over real HTTP/1.1, HTTP/2
and TLS. It is its own Go module, so `go test ./...` never runs it. It needs a
running Docker daemon and is opt-in:

```bash
DIVISOR_INTEGRATION=1 go -C integration-test test -v -timeout 20m .
```

Each scenario runs in parallel on its own Docker network. Why the suite is
shaped this way is in
[ADR 0001](development/adr/0001-black-box-integration-suite.md) and
[ADR 0002](development/adr/0002-spec-red-gating.md).

## Lint

CI runs golangci-lint v2.15.0 with the repo's `.golangci.yml`:

```bash
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run --timeout=3m
```

## Changing the Middleware contract

The `middleware/` package is what Middleware authors import, and divisor
exposes it to the yaegi interpreter through generated symbols. After any change
to it, regenerate them and commit the result:

```bash
go generate ./pkg/middleware/
```

## Docs that tests keep in sync

Some user docs are checked by unit tests, so a change to config, logging or
Middleware fails `go test` until the docs follow:

| You changed | Update | Checked by |
| --- | --- | --- |
| A config key (added, removed, renamed) | Its row in the [`docs/configuration.md`](docs/configuration.md) reference tables (key, type, default, since, reload, description) | `TestConfigurationReferenceDocumentsEveryKeyTheParserAccepts` |
| A config key used by an example | Every `examples/*.yaml`; the quickstarts and `examples/systemd/config.yaml` must also pass validation | `TestExamplesParseUnderStrictKeys`, `TestExamplesRunAsIsPassValidation` |
| The Kubernetes example config or the Helm chart's default config | Both: `examples/kubernetes/configmap.yaml` and `charts/divisor/values.yaml` ship the same config | `TestKubernetesExampleConfigMapHoldsAValidConfig`, `TestHelmChartConfigMatchesTheKubernetesExample` |
| An Access log field | The fields table in [`docs/observability.md`](docs/observability.md#access-log-fields) | `TestObservabilityGuideDocumentsEveryAccessLogField` |
| `middleware/middleware.go` or the example chain | Every Go code block in [`docs/middleware.md`](docs/middleware.md) must be a verbatim copy from `middleware/middleware.go` or `examples/middleware-chain.config.yaml`; inline Middleware in examples must compile | `TestMiddlewareGuideGoBlocksAreCopiedFromCompiledCode`, `TestMiddlewareGuideExampleChain`, `TestExampleInlineMiddlewaresCompile` |

Files in `examples/` that are not divisor configs (the Kubernetes Deployment
and Service, the systemd unit, the observability stack's Compose, Prometheus
and Alloy files) have no test: check them by hand.

`docs/` is for people who use divisor. Keep it free of ADR links, test names
and CI details, and explain a term in the guide where it first appears.

## Code style

Code explains itself: put the meaning in names and structure. A comment earns
its place only for a constraint the code cannot show (a non-obvious library
behavior, a spec decision, a pointer to an ADR), and stays to one or two lines.

Names are explanatory and written out in full: `StartHealthChecker` not
`Start`, `updateLiveness(backend, isAlive)` not `apply(b, ok)`,
`aliveBackendCount` not `n`. Idiomatic one-letter receivers and loop indices
are the only short names.

## Domain vocabulary

[`CONTEXT.md`](CONTEXT.md) defines the project's terms (Backend, Pool,
Balancer, Probe, Rejoin, Reload, Short-circuit, ...) and the synonyms to avoid.
Use those terms in code, tests, commit messages and docs. If a concept you need
is missing, add it there in the same change.

## Architecture decision records

A decision that is hard to reverse, surprising without its context, or the
result of a real trade-off gets an ADR in [`development/adr/`](development/adr/):
a new file numbered after the last one (`0008-short-title.md`) that states the
decision, the options considered and why they lost, and the consequences.
Amend an ADR by writing a new one that says what it changes; don't rewrite
history. Point to the ADR from the code it constrains in a one-line comment.

## Pull requests

1. For anything beyond a small fix, open a GitHub issue first so the approach
   can be agreed before you write it.
2. Branch from `main`, keep the change focused, and run `go test ./...` and the
   linter before pushing.
3. Commit subjects follow `type(scope): summary`, with the scope optional:
   `feat: ...`, `fix(proxy): ...`, `refactor(core): ...`, `docs: ...`,
   `ci(review): ...`.
4. Open a PR against `main`. CI runs the linter, the unit tests with `-race`,
   the integration suite, and, when the PR touches release files
   (`.goreleaser.yaml`, `Dockerfile`, `main.go`, `charts/`, `.github/scripts/`
   or the release workflows), a release dry-run.

Instructions for AI coding agents working in this repo are in
[`development/agents/`](development/agents/).
