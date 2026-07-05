# helixon-eval

Standalone evaluation harness for Helixon agents. Migrated from
`github.com/nfsarch33/helixon-platform/internal/evalfw` in v16202 of the
v16201-v16300 plan (CARRY-056 closure).

## Layout

- `cmd/helixon-eval/` — CLI binary (`run`, `report`, `list` subcommands)
- `internal/evalfw/` — core framework (Case, Suite, Runner, ReportWriter)
- `internal/rubric/` — 5 evaluation rubrics (reliability, observability,
  security, test coverage, task completion)
- `testdata/` — golden fixtures

## Subcommands

```
helixon-eval run     --suite <name> [--output /tmp/report.ndjson]
helixon-eval report  --input /tmp/report.ndjson
helixon-eval list    [--rubric <name>]
```

## Rubrics (v16202 R4)

1. **Reliability** — passes under `-race` detector + retries transient
   failures up to 3 times before reporting VerdictFail.
2. **Observability** — every Case emits at least one metric; ReportWriter
   writes NDJSON to ~/logs/runx/eval-results.ndjson.
3. **Security** — no secrets on argv (lint enforced by `helixon-eval lint`);
   case inputs sourced from fixtures only.
4. **Test coverage** — internal/evalfw package coverage ≥ 90% (gate
   enforced by CI).
5. **Task completion** — every Case returns Verdict ∈ {Pass, Warn, Fail};
   no panic, no unhandled error.

## Build & test

```
go test -race -count=1 ./...
go test -coverprofile=coverage.out ./internal/evalfw/...
go tool cover -func=coverage.out | grep total
```

## Migration provenance

Migrated from commit `e1005ff` on `feature/v16129-helixon-eval` of
helixon-platform. Original package names preserved as `evalfw` to keep
the migration surgical; future R5+ may rename to `eval` if no external
dependents exist.

— cursor-parent (jaslian@gmail.com)
   v16202-v16300 sprint 2