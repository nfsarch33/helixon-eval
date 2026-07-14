// Package agenteval is the integration layer between the Helixon Agent
// runtime (checkpoint + livechannel, in nfsarch33/helixon-platform) and
// the helixon-eval harness (internal/evalfw + internal/rubric).
//
// v18628-1 contract: a Helixon Agent run is wrapped as an evalfw.Suite
// where each Case is a single (task, model) pair and the entire matrix
// runs through the same harness that already enforces the 5 production
// rubrics (reliability, observability, security, test_coverage,
// task_completion).
//
// The integration guarantees:
//
//  1. **Model matrix coverage** — every run is a 5×3 cross product of
//     canonical Helixon tasks and the three production models
//     (Minimax-M3, qwen3.7-plus, qwen3.7-max). Missing rows fail the
//     suite (matrix drift guard).
//
//  2. **Rubric compatibility** — the agent suite reuses rubric.All and
//     evaluates every run through that registry; agents that drop a
//     rubric fail the run, matching the v16129 invariants.
//
//  3. **CODEMAP freshness** — the suite reads helixon-platform/CODEMAP.md
//     and asserts the file exists, is non-empty, and has been modified
//     within FreshnessTTL. A stale CODEMAP degrades the verdict to WARN
//     so the operator knows the canonical surface map is drifting.
//
//  4. **Loopguard** — every Case function must return within
//     LoopguardTimeout or fail with a "loop guard tripped" error.
//     Pair with the existing reliability rubric so agents that spin
//     on a bad tool call cannot exhaust the eval budget.
//
// The package exposes SuiteForRun(...) so the helixon-eval CLI can run
// an agent eval without code changes in the harness binary.
package agenteval
