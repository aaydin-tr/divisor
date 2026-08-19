# Spec-red gating: spec-red tests are opt-in via a second env var

> **Status note (2026-08):** both original spec-red tests shipped (bounded
> failure for hanging Backends via `server.proxy_timeout`, 413 via
> `server.max_request_body_size`) and now run in the blocking job. The spec-red
> set is empty, so the advisory CI job was removed from
> `.github/workflows/integration.yml`; the `specRed` helper and
> `DIVISOR_INTEGRATION_SPEC_RED` env var remain dormant. When the next spec-red
> test lands, re-add the advisory job as described below.

The integration suite deliberately contains born-red tests: they assert agreed 1.0 spec behavior that has not shipped yet (bounded failure for hanging Backends, 413 for oversized bodies). Left as-is, they turned the Integration CI job red on every PR, which trains everyone to ignore the job and hides genuine regressions. We split the two signals: born-red tests call `specRed(t, ...)` and skip themselves unless `DIVISOR_INTEGRATION_SPEC_RED=1`, so the blocking Integration job runs only tests expected to pass, while a second advisory job (`continue-on-error: true`) runs just the spec-red tests as a tracker of the spec gap. (GitHub reports a job-level `continue-on-error` failure as a successful check in the PR checks list; the red failure is visible inside the workflow run's job graph and logs, not as a red check.) When a spec item ships, its test drops the `specRed` marker and thereby moves into the blocking job.

The advisory job selects the spec-red tests with a hardcoded `go test -run` pattern in `.github/workflows/integration.yml`; adding a new `specRed` marker means adding the test's name to that pattern too.

## Considered Options

- **Leave the Integration job red until 1.0 ships** — rejected: a permanently red required check cannot gate anything, and every real regression looks like the known red.
- **Skip via `testing.Short()` / build tags** — rejected: `-short` inverts the convention (born-red tests are the long/strict ones only in spirit), and build tags hide the tests from `go vet`/editors; an explicit env var matches the suite's existing `DIVISOR_INTEGRATION` opt-in style.
- **Advisory job runs the whole suite with the flag set** — rejected: doubles CI time to re-run tests the blocking job already covered; the advisory job only needs the red set.

## Consequences

- The blocking Integration job is green on a healthy PR; a red there is always actionable.
- The spec gap is tracked in every PR by the "Integration (spec-red, advisory)" job without blocking merges — but its check shows green even when the tests fail (job-level `continue-on-error` semantics); the actual red lives one click deeper, in the workflow run's job graph.
- Two bookkeeping duties when marking a test spec-red: add the `specRed` call and extend the advisory job's `-run` pattern; the reverse when a spec item ships.
- Local runs with only `DIVISOR_INTEGRATION=1` show the spec-red tests as skipped, with the skip message naming the missing behavior.
