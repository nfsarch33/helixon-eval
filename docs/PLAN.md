# HelixonEval R4 Plan

_Date_: 2026-07-05T23:05+10:00

## Goal

Stand up the `nfsarch33/helixon-eval` standalone repository, porting
the minimal evaluation harness from `helixon-platform/internal/evalfw`
and adding 5 first-class rubrics with a CLI entry point.

## Rubrics

1. **reliability** — passes under -race; retries transient failures
2. **observability** — every Case emits at least one metric
3. **security** — no secrets on argv; case inputs sourced from fixtures only
4. **test_coverage** — internal/evalfw coverage ≥ 90% (CI gate)
5. **task_completion** — every Case returns Verdict ∈ {Pass, Warn, Fail}

## CLI

```
helixon-eval run --suite <rubric-name>
helixon-eval report
helixon-eval list
```

## Quality Bar

- `go test -race -count=1 ./...` GREEN
- Coverage on `internal/evalfw` ≥ 90%
- Sentrux gate: no degradation vs baseline (6791)
- 0 cycles, 0 god files

## Migration Provenance

- Source: `helixon-platform/internal/evalfw/` (eval.go, report.go)
- Source CLI: `helixon-platform/cmd/helixon/`
- Destination: `nfsarch33/helixon-eval/{internal/evalfw,internal/rubric,cmd/helixon-eval}`
- Branch: `r4-eval-harness`