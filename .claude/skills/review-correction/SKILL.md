---
name: review-correction
description: Use when working through a batch of code-review findings on divisor — from the user, the "Auto review PRs" Claude workflow, a PR review, a correction doc in `.scratch/`, or a review tool — to decide which to fix, push back on, or defer, and to land the accepted fixes safely. Triggers include "handle this review", "address these comments", a numbered list of findings/suggestions, or pasted PR review feedback.
---

# Review Correction

Work a batch of review findings to a disposition (fix / push back / defer / ask) and land the accepted fixes without regressions. **A finding is a claim to verify, not an order to execute.** Almost every batch contains at least one finding that is stale, already done, out of scope, settled by an ADR, or wrong for this codebase.

**Core principle:** Verify every finding against the CURRENT code — and the CURRENT dependency source — before touching anything. Give each one an explicit verdict. Then fix one finding at a time, running its covering test after each.

## Reception rules

- **One unclear finding blocks the whole batch.** Findings are often related, so a partial understanding produces a wrong implementation. If any item is unclear, implement nothing yet — list what you understood and ask about the rest in one message.
- **Trust depends on the source.** The user's own feedback is implemented once understood (still ask if scope is unclear). An external or automated reviewer (the `review.yml` Claude workflow included) lacks this repo's history: check each claim against the code, `CONTEXT.md`, and `development/adr/`. A finding that contradicts an ADR or a decision the user already made gets raised, not implemented — name the ADR (_"Contradicts ADR 0004 (plaintext-only Backends)…"_).
- **Ask with evidence and a recommendation.** An Ask goes to the user as a short question: the finding, the evidence (source line, wire behavior, test output), two options, the recommended one first. Batch them into one round.
- **If you can't verify a finding, say so.** "I can't verify this without [X] (e.g. the integration suite, `-race`, a Linux box) — investigate, ask, or proceed?" beats silently implementing or silently dropping it.
- **Conceding a wrong pushback takes one sentence.** "You were right — checked [X], it does [Y]. Fixing." No apology, no defence of the original position.
- **On GitHub, reply in the inline thread**, not as a top-level PR comment: `gh api repos/aaydin-tr/divisor/pulls/{pr}/comments/{id}/replies`. Posting is outward-facing: show the user the replies and post only on their go-ahead.

## Workflow

1. **Triage every finding FIRST — before editing anything.** For each finding, open the cited code and confirm the claim. The review's line numbers and "current code" snippets are often STALE (the file moved since the review ran) — locate the real code, don't trust the citation. **The "current code" is bigger than the cited snippet:** the behavior a finding calls "missing" often lives one layer away:

   | Finding says X is missing in… | Look first in… |
   |---|---|
   | proxy forwarding (body limits, Content-Length, chunked, trailers, timeouts) | the dependency's own source at the pinned version — `go list -m -f '{{.Dir}}' github.com/valyala/fasthttp`, and net/http for the HTTP/2 path. The listener stack may already guarantee it |
   | a config field's default or validation | `PrepareConfig()` in `pkg/config`; strict decoding (`KnownFields`) already rejects unknown keys |
   | a Balancer's handling of Down Backends | the Pool: liveness, Probes and Join/Leave belong to `core/pool`, and a Balancer only selects |
   | a Reload applying some key | `PlanReload`'s reloadable set (ADR 0006): everything outside `backends` / `health_checker_*` is restart-only on purpose |
   | something both listener stacks need | the shared path: `NetHttpAdapter` bridges HTTP/2 into the same `fasthttp.RequestCtx`, so one fix usually covers both |
   | a "should we also handle Y" | `CONTEXT.md` and the ADRs — Y may be a recorded non-goal |

   Assign one verdict:

   | Verdict | When |
   |---|---|
   | **Fix** | The claim is real and the change is warranted for THIS codebase. |
   | **No change** | Already done, no longer applies, false positive, unreachable from any client, or correct-as-is. State the reason. |
   | **Defer / flag** | Real but out of scope — pre-existing, shared code, or a product/policy call. Flag it; don't fix it piecemeal. |
   | **Ask** | Genuinely the user's decision (scope, policy, an ADR to reopen, a trade-off the code can't settle). |

   Present the verdict table to the user before or alongside the fixes. Severity labels (Critical/High/Medium/…) rank attention, not obligation — a "Critical" can still be a No-change if it's already handled, and a "Minor" can be a real Fix.

2. **Order the accepted fixes:** security/correctness → simple → refactors.

3. **Fix ONE finding at a time, then run its covering test.** A "trivial" or "behavior-preserving" edit can still be wrong — only the test proves it (e.g. a refactor that changes a signature but not the body still compiles and still ships the old behavior). Never batch several fixes and verify once at the end; a failure then can't be localized. A Fix that the existing tests can't observe gets a test that goes red without it.

4. **Verify at the layer where the bug lives.** Don't claim a fix works from a layer that can't observe it.

   | Finding touches | Verify with |
   |---|---|
   | one Balancer's selection | that balancer package's `*_test.go` (feed `Join`/`Leave`, assert `Pick`) |
   | liveness, Probe rounds, Reload placement | `core/pool/pool_test.go` (`NewPool` + `ProbeAllBackends`, `RecordingBalancer`; the real ip-hash Balancer when ring placement is the point) |
   | wiring, `Serve` forward vs 503, `Pick` racing a Probe round | `core/balancer_test.go` |
   | proxy headers, forwarding, Score / Trial request | `internal/proxy/proxy_test.go` |
   | Streamed responses, Access log | `streaming_test.go` / `access_log_test.go` — on `onEveryListenerStack`, never one stack only |
   | config validation / Reload classification | `pkg/config/config_test.go` / `pkg/config/reload_test.go` |
   | Reloader, config file watch | `internal/reload/reload_test.go` / `watch_test.go` |
   | anything shared across goroutines (Pool, Rotation, ring, proxy counters) | `go test -race` on that package — needs cgo; where it's unavailable, run it in a `golang` container or report that only CI checked it |
   | listener behavior: timeouts, body size limits, TLS/HTTP2, signals, exit codes, ConfigMap Reload, startup with Backends down | the **integration suite** (`DIVISOR_INTEGRATION=1 go -C integration-test test -v -timeout 20m .`, Docker up — check `docker info`) — unit tests call the handler directly and bypass the real listener |
   | the `middleware/` contract (yaegi) | `go generate ./pkg/middleware/`, then build and grep `middleware_symbols.go` |
   | `benchmark/` | `go -C benchmark test -race ./...`; a report change re-renders `report/testdata/` with `-update` — review that diff |
   | Helm chart or `examples/kubernetes` | change both; `TestHelmChartConfigMatchesTheKubernetesExample`, and `.github/scripts/helm-chart-smoke-test.sh` where helm is installed |
   | `docs/`, `README.md`, `examples/` | the docs-by-audience rule in `CLAUDE.md`: user-facing, no ADR / `CONTEXT.md` / test-name links |

5. **Final gate + report dispositions.** Run `go build ./...`, `gofmt -l .`, `go vet ./...`, lint the changed packages with the CI version (`golangci-lint run ./<changed-pkgs>/...`), and the relevant test layers from step 4. Report each finding's outcome — fixed / pushed back (with reason) / deferred / asked — and which checks ran locally vs only in CI. No "done" without the per-finding disposition. Leave the changes in the working tree and close with a suggested commit message, per `CLAUDE.md`.

## Don't expand scope

A finding about **pre-existing or shared code** gets **flagged, not silently fixed**. In divisor that's mostly: `pool.Rotation` (embedded by every slice-based Balancer), `pkg/consistent`, `pkg/helper`, the proxy path both listener stacks share, and the `middleware/` contract authors build against. Fixing it piecemeal creates inconsistency and risks unrelated callers — patching one Balancer while its siblings keep the same shape is worse than leaving them consistent. Surface it to the user for a dedicated decision, and offer to record it as an issue under `.scratch/<feature>/issues/` (see `development/agents/issue-tracker.md`). Match the fix to the finding you own; resist "while I'm here."

## Red flags — STOP

- About to edit before locating the real current code → verify the finding first; it may be stale or already done.
- Implementing a finding *because* it's labeled Critical, without judging it → severity ≠ obligation.
- Batching fixes to test once at the end → fix one, test one.
- "It's a trivial/behavior-preserving rename, skip the test" → run it; the test is where the typo surfaces.
- "I'll harden the shared thing while I'm here" → scope creep; flag it instead.
- About to add a guard in `internal/proxy` for a malformed request → first prove a client can produce it through fasthttp or net/http at the pinned versions; most "missing guards" are unreachable.
- About to teach a Balancer about liveness, or a Pool about selection → the split is the architecture; the fix belongs on the other side.
- About to make a restart-only key reloadable, add HTTPS Backends, or change ip-hash placement → that reopens an ADR; Ask.
- Typing "Thanks, great catch, fixed!" → drop the performative agreement; state the disposition.

## Common mistakes

| Mistake | Fix |
|---|---|
| Trusting the review's line numbers / snippets | Locate the real code; citations drift. |
| Implementing an already-satisfied finding | Verify current state first; mark No-change. |
| Reasoning about fasthttp from memory or docs | Read the source at the version in `go.mod`; behavior moves between releases. |
| One edit / one fixer for the whole batch | One finding at a time, test each. |
| Claiming a listener-level fix passes from a unit test | Run the integration suite, or say it wasn't run. |
| Fixing a streaming bug on one stack | `onEveryListenerStack` — fasthttp, net/http HTTP/2, net/http HTTP/1.1. |
| Editing `middleware/` without regenerating | `go generate ./pkg/middleware/`; an added symbol stays invisible to interpreted middleware until regenerated, and no build catches that. |
| Silently fixing a pre-existing shared-code issue | Flag for a separate decision; don't piecemeal. |
