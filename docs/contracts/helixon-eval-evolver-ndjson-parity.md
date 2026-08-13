# helixon-eval-evolver-ndjson-parity Regression Contract (q8-c-10, ADR-0008 IMP-001)

**Sprint:** v18750-Q8 (carry-in)
**ADR:** [ADR-0008-helixon-eval-vs-evolver-separation.md](../../../cursor-global-kb/adrs/ADR-0008-helixon-eval-vs-evolver-separation.md)
**Status:** LOCKED
**Date locked:** 2026-08-13

## Purpose

This is the canonical regression-contract lock doc for the
helixon-eval ⇄ helixon-evolver NDJSON contract. It exists so a future
contributor cannot silently mutate the shared schema, the required
field set, or the cross-repo parity invariant without explicitly
revoking this lock and bumping `schema_version`.

The companion regression test lives at
`internal/contract/ndjson_parity_test.go` in `helixon-eval`. It is
expected to be duplicated (byte-for-byte) into `helixon-evolver` and
run on every CI build of both repos.

## The Contract (LOCKED)

| Field | Type | Required | Notes |
|----|----|----|----|
| `schema_version` | string | YES | Semver `MAJOR.MINOR.PATCH`. Bump MAJOR on breaking changes. |
| `job_id` | string | YES | Stable across retries. |
| `eval_id` | string | YES | One-shot per eval invocation. |
| `tenant_id` | string | YES | Cross-tenant isolation marker. |
| `model_id` | string | YES | Model identifier (e.g. `MiniMax-M3`, `qwen3.7-plus`). |
| `rubric_version` | string | YES | Bumped on rubric-set change. |
| `started_at` | string (ISO 8601) | YES | UTC, RFC 3339. |
| `finished_at` | string (ISO 8601) | YES | UTC, RFC 3339. |
| `status` | enum | YES | One of: `success`, `error`, `timeout`, `cancelled`. |
| `metrics` | object | YES | Free-form metric payload; structure evolves under `schema_version`. |

Top-level `additionalProperties: false`. Any new top-level field
requires a `schema_version` MAJOR bump.

## Repo Locations

| Repo | Path |
|----|----|
| `helixon-eval` | `contracts/ndjson-eval-result.schema.json` |
| `helixon-eval` | `internal/contract/ndjson-eval-result.schema.json` (embedded) |
| `helixon-evolver` | `contracts/ndjson-eval-result.schema.json` |

The two contracts/ copies MUST remain byte-identical (sha256 match).
The embedded copy in `internal/contract/` MUST match the on-disk
contracts/ copy in the same repo.

## Bumping the Contract

1. Open a new issue: `[contract-bump] helixon-eval-evolver-ndjson-parity`
2. Update `schema_version` in BOTH repos' `contracts/ndjson-eval-result.schema.json`
3. Update this lock doc to record the new `schema_version`
4. Open a single PR that touches both repos; require review from
   the eval-evolver contract owner (per `cursor-global-kb/CODEOWNERS`)
5. CI must run `go test -count=1 ./internal/contract/...` in both
   repos with exit 0

## What This Contract Blocks

- Silent schema drift between eval (emitter) and evolver (consumer).
- Required-field removal without a `schema_version` bump.
- Top-level field additions (the `additionalProperties: false` rule
  enforces this at parse time; the regression test enforces it at
  build time).
- JSON-malformed schema files (rejected at unit-test time).

## Evidence

- `internal/contract/ndjson_parity_test.go` in `helixon-eval` (6
  passing tests; 1 skip that documents the lock file path).
- Mirror test file expected in `helixon-evolver` (q9-c-future).
- `evidence/v18750-Q9/q9-3-ndjson-parity.md` — closeout record.
