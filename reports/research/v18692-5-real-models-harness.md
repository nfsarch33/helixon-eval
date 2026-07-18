# v18692-5 — Real-Models Harness for helixon-eval

**Sprint:** v18692 (sprint 5 of 7)
**Date:** 2026-07-18
**Author:** cursor-parent (helixon-platform owner via shared plan)
**Branch:** `qa/v18692-5-eval-harness-real-models`
**Repo:** `helixon-eval`
**Machine-Id:** win3-wsl3

## Story

**As a** Helixon QA harness operator,
**I want** a multi-provider real-models harness that runs the canonical
7-task pilot demo across MiniMax-M3, qwen3.7-plus, and qwen3.7-max,
**So that** every sprint can produce a comparable cost-dashboard and
model-comparison matrix without manually wiring three LLM clients.

## What shipped

1. **`internal/demo/harness.go`** — multi-provider matrix harness:
   - `Provider` enum with canonical IDs (`minimaxi`, `qwen3.7-plus`,
     `qwen3.7-max`) plus aliases (`minimax`, `MiniMax-M3`, `qwen`,
     `qwen-max`).
   - `PricingFor(p)` — per-provider pricing schedule reflecting the
     v18681-5 baseline.
   - `RunMatrix(ctx, providers, apiKeys, useLive)` — runs the canonical
     7-task demo per provider, returns one `MatrixRow` per provider
     with `Status` (`ok` / `skipped` / `fail`) and live task rows.
   - `AppendNDJSON(path, result)` — appends the matrix envelope to a
     trend file (default `~/logs/runx/helixon-eval-matrix.ndjson`).
   - `RenderMarkdown(result)` — cost dashboard + model comparison
     matrix, ASCII-sorted cheapest first.
   - `ResolveAPIKeysFor(providers, override, env)` — env-first key
     resolution with override precedence.
   - `RoundTrip(ctx, p, key, prompt)` — single-call smoke helper for
     ad-hoc operator health checks (built on top of `llmclient.New`).
   - Live mode is gated on `HELIXON_LIVE_EVAL=1` or `--live`. Without
     the gate the harness runs in `dry-run` mode and never dials out.

2. **`cmd/demo/main.go`** extended with the v18692-5 surface:
   - `--provider minimaxi|qwen3.7-plus|qwen3.7-max`
   - `--all-providers` runs the 3-row matrix
   - `--matrix` skips the v18688-2 single-pilot back-compat path
   - `--live` / `HELIXON_LIVE_EVAL=1` toggles real round-trip
   - `--api-key` overrides env for the target provider
   - `--ndjson-out` default `~/logs/runx/helixon-eval-matrix.ndjson`
   The legacy v18688-2 pilot path is preserved for callers that don't
   pass any v18692-5 flags.

3. **`internal/demo/harness_test.go`** — 20 RED tests covering:
   - `ParseProvider` canonical IDs + aliases + bad-input error
   - `Provider.Model / Backend / EnvKey` round-trip
   - `PricingFor` realistic per-provider rates
   - `RunMatrix` dry-run, live-without-key → `skipped`, rejects empty
   - `RenderMarkdown` sections + ascending cost ranking
   - `AppendNDJSON` writes 2 lines that round-trip through JSON
   - `EnvKeyMap` / `ResolveAPIKeysFor` env-vs-override precedence
   - `RoundTrip` empty-key error path
   - `MatrixResult.TotalCost` aggregates per-row costs
   - Started-before-Completed timing invariants

## Test evidence (race detector on)

```
$ go test -race -count=1 ./...
?   	github.com/nfsarch33/helixon-eval/cmd/demo	[no test files]
ok  	github.com/nfsarch33/helixon-eval/cmd/helixon-eval	6.084s
ok  	github.com/nfsarch33/helixon-eval/internal/agenteval	2.073s
ok  	github.com/nfsarch33/helixon-eval/internal/demo	1.026s	coverage: 75.6%
ok  	github.com/nfsarch33/helixon-eval/internal/evalfw	1.043s
ok  	github.com/nfsarch33/helixon-eval/internal/idempotency	1.026s
ok  	github.com/nfsarch33/helixon-eval/internal/llmclient	1.025s
ok  	github.com/nfsarch33/helixon-eval/internal/llmcost	1.018s
ok  	github.com/nfsarch33/helixon-eval/internal/rubric	1.119s
ok  	github.com/nfsarch33/helixon-eval/internal/tenancy	1.035s
```

All 9 packages GREEN with `-race`. Coverage of `internal/demo` at 75.6%
(above the v18688-2 baseline).

## Live round-trip (MiniMax-M3)

```
$ export MINIMAX_API_KEY=$(op read op://HelixonSafe/ripotpfq43jzlreor4zo2ay734/api-key)
$ go run ./cmd/demo --all-providers --matrix --live --ndjson-out /tmp/matrix-live.ndjson

| Provider | Model | Status | Tasks | Total Cost |
|---|---|---|---|---|
| minimaxi | MiniMax-M3 | ok | 7 | $0.001884 |
| qwen3.7-plus | qwen3.7-plus | skipped | 7 | $0.005928 (dry-run) |
| qwen3.7-max  | qwen3.7-max  | skipped | 7 | $0.009880 (dry-run) |
```

Live MiniMax-M3 token counts (measured):

| Task | Tokens in/out | Latency | Cost |
|---|---|---|---|
| echo | 185 / 54 | 2.482s | $0.000256 |
| lookup | 185 / 64 | 2.985s | $0.000276 |
| multi-turn | 186 / 64 | 1.621s | $0.000277 |
| tool-call | 186 / 50 | 2.447s | $0.000249 |
| plan-execute | 187 / 62 | 2.272s | $0.000274 |
| retry | 185 / 64 | 1.524s | $0.000276 |
| long-context | 186 / 64 | 1.867s | $0.000277 |
| **Sum** | | | **$0.001884** |

NDJSON envelope written to `/tmp/matrix-live.ndjson` (7021 bytes, 1
valid JSON row). A typical trend file will accumulate one line per
run; the agentrace/observability pipeline (CF-187) will add per-line
ingestion.

## Honest KPI (per DRL-8.20-r4)

- **VERIFIED axes:** build GREEN, all 9 packages GREEN with -race,
  live MiniMax-M3 7-task round-trip, NDJSON envelope written.
- **CLAIMED axes:** qwen3.7-plus / qwen3.7-max live status. Verifier
  has not yet wired `QWEN_API_KEY` from 1Password; dry-run pricing
  shown above is the planning default.
- **UNVERIFIED axes:** `coverage ≥ 90%` on `internal/demo` (current
  75.6% — 20 new tests exercise the public surface; the gap is in the
  legacy `cost.go` helpers not touched by v18692-5). Carry-forward.

## Carry-forward (add to next sprint)

1. **`qwen3.7-plus / qwen3.7-max live verification`** — script must
   `op read` `op://HelixonSafe/4qt774avrbzabdscc6ezygl5hi/credential`
   (or similar) and inject as `QWEN_API_KEY`. Per the operator pair-
   rotation rule, both halves of any Qwen item must be verified before
   that item is wired to live round-trip.
2. **`internal/demo` coverage ≥ 90%`** — add unit tests against the
   legacy `ComputeCost / P99Latency / JobID` paths to push coverage
   above the v18688-2 baseline.
3. **NDJSON trend ingestion** — wire `~/logs/runx/helixon-eval-matrix.ndjson`
   into the agentrace observability pipeline so the KPI report can
   surface per-sprint cost-per-task trend (CF-187).

## Quality gates

| Gate | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go test -race ./...` | 9/9 packages GREEN |
| `go test -cover ./internal/demo/...` | 75.6% (carry for ≥90%) |
| Live MiniMax-M3 round-trip | 7/7 tasks ok, $0.001884 total |
| NDJSON envelope written | 7021 bytes, valid JSON |
| Legacy v18688-2 path preserved | yes (default `go run ./cmd/demo`) |

## References

- Sprint plan: `~/.cursor/plans/helixon_v18691-v18694_pilot+qa+reality+workspace_e406210d.plan.md`
  (§ v18692-5)
- Worktree: `~/runs/worktrees/helixon-eval/qa-v18692-5-eval-harness-real-models`
- Remote branch: `origin/qa/v18692-5-eval-harness-real-models`
- Demo entrypoint: `cmd/demo/main.go`
- Harness: `internal/demo/harness.go`
- Tests: `internal/demo/harness_test.go`
- Carry-forward: `global-kb/cursor-config/cf-register-2026-07-18.ndjson` (pending)

Machine-Id: win3-wsl3
