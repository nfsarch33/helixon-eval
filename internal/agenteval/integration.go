package agenteval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/rubric"
)

// runAgentMu serialises reads/writes of the RunAgent hook so tests that swap
// the global hook in t.Cleanup do not race against goroutines spawned by
// caseForTask. CARRY-182 (race on integration_test.go:132 vs integration.go:160).
var runAgentMu sync.Mutex

// ProductionModels are the three models every Helixon Agent run must
// cover. Adding a fourth model is a deliberate change: bump the constant
// slice and update the 5×3 matrix assertions in agent_integration_test.go.
var ProductionModels = []string{"MiniMax-M3", "qwen3.7-plus", "qwen3.7-max"}

// CanonicalTasks is the fixed task set the agent eval matrix runs.
// Order is intentional (stable ordering for verdict reporting).
//
// v18681-3 added 2 multi-step coding tasks (idempotency_key_chain and
// json_diff_reconciliation) to bring the matrix to 7 tasks. The 5-task
// matrix (5×3=15 cases) was the v18671-1 baseline; the 7-task matrix
// (7×3=21 cases) is the v18681+ baseline. Task IDs are kebab-case and
// stable for verdict reporting and cross-sprint comparisons.
var CanonicalTasks = []string{
	"tool_dispatch_correctness",   // v17600
	"checkpoint_emits_metrics",    // v17600
	"livechannel_open_close",      // v17600
	"loopguard_trips_on_infinite", // v17600
	"rubric_compatibility_check",  // v17600
	"idempotency_key_chain",       // v18681-3 (NEW; multi-step coding)
	"json_diff_reconciliation",    // v18681-3 (NEW; multi-step coding)
}

// FreshnessTTL bounds how stale a CODEMAP.md can be before the suite
// warns. v17805 baseline picks 30 days — long enough to ignore
// weekend-only changes, short enough to catch real drift.
const FreshnessTTL = 30 * 24 * time.Hour

// LoopguardTimeout caps any single Case's wall-clock. Anything longer
// is classified as a runaway loop (see TestLoopguard_TripsOnLongCase).
const LoopguardTimeout = 5 * time.Second

// LoopguardError is returned by Case functions when the agent exceeded
// the per-case budget.
var LoopguardError = errors.New("agenteval: loop guard tripped (case exceeded LoopguardTimeout)")

// ErrNoProductionModels is returned when the ProductionModels slice is
// empty; the matrix contract requires ≥ 3 models.
var ErrNoProductionModels = errors.New("agenteval: ProductionModels must include MiniMax-M3, qwen3.7-plus, qwen3.7-max")

// ErrMissingCODEMAP is returned when helixon-platform/CODEMAP.md cannot
// be read; the freshness gate cannot be evaluated without it.
var ErrMissingCODEMAP = errors.New("agenteval: helixon-platform/CODEMAP.md not found or empty")

// ErrStaleCODEMAP carries the age of the CODEMAP at the time of the
// check; the caller decides whether to degrade to WARN or hard-fail.
type ErrStaleCODEMAP struct {
	Age time.Duration
	TTL time.Duration
}

func (e *ErrStaleCODEMAP) Error() string {
	return fmt.Sprintf("agenteval: CODEMAP.md age %s exceeds freshness TTL %s", e.Age, e.TTL)
}

// Config controls one agent-eval run.
type Config struct {
	// CodeMapPath is the absolute path to helixon-platform/CODEMAP.md.
	// Defaults to "$HOME/Code/helixon-platform/CODEMAP.md" when empty.
	CodeMapPath string
	// Now is injected for testability; defaults to time.Now.
	Now func() time.Time
	// LoopguardTimeout overrides the package default for slower
	// suites (e.g., production-pilot runs).
	LoopguardTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.CodeMapPath == "" {
		home, _ := os.UserHomeDir()
		c.CodeMapPath = filepath.Join(home, "Code", "helixon-platform", "CODEMAP.md")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.LoopguardTimeout <= 0 {
		c.LoopguardTimeout = LoopguardTimeout
	}
	return c
}

// SuiteForRun assembles the 5×3 eval matrix as an evalfw.Suite. Every
// row in the matrix is a (task, model) Case; the suite name is
// "agent-eval-<runID>" so report NDJSON files can be correlated.
func SuiteForRun(runID string, cfg Config) (evalfw.Suite, error) {
	cfg = cfg.withDefaults()

	if err := validateProductionModels(ProductionModels); err != nil {
		return evalfw.Suite{}, err
	}

	cases := make([]evalfw.Case, 0, len(CanonicalTasks)*len(ProductionModels))
	for _, task := range CanonicalTasks {
		for _, model := range ProductionModels {
			t, m := task, model
			cases = append(cases, evalfw.Case{
				Name: fmt.Sprintf("%s__%s", t, m),
				Tags: []string{"agenteval", task, model},
				Fn:   caseForTask(t, m, cfg),
			})
		}
	}

	return evalfw.Suite{
		Name:  fmt.Sprintf("agent-eval-%s", runID),
		Cases: cases,
	}, nil
}

func validateProductionModels(models []string) error {
	if len(models) < 3 {
		return ErrNoProductionModels
	}
	want := map[string]bool{"MiniMax-M3": false, "qwen3.7-plus": false, "qwen3.7-max": false}
	for _, m := range models {
		if _, ok := want[m]; ok {
			want[m] = true
		}
	}
	for m, present := range want {
		if !present {
			return fmt.Errorf("%w: missing %s", ErrNoProductionModels, m)
		}
	}
	return nil
}

// caseForTask maps a canonical task to a Case function. The "stub"
// tasks run a deterministic synthetic check; real agent integration
// plugs in here via the RunAgent hook (kept as a function variable
// for testability and to avoid an import cycle with helixon-platform).
type agentHook func(ctx context.Context, task, model string) (passed bool, metrics map[string]float64, err error)

// RunAgent is the seam where the live Helixon Agent runtime would
// dispatch a task. Tests override it; production wires it to
// helixon-platform/internal/agent/checkpoint.
//
// Reads and writes are serialised by runAgentMu so test t.Cleanup
// swapping the hook does not race against caseForTask's spawned
// goroutines (CARRY-182 fix).
var RunAgent agentHook = func(ctx context.Context, task, model string) (bool, map[string]float64, error) {
	return true, map[string]float64{"task": hashTask(task), "model_hash": hashModel(model)}, nil
}

// callRunAgent invokes RunAgent under runAgentMu so the spawned goroutine
// reads the hook atomically. Tests that swap the hook must also hold the
// lock (use SetRunAgent).
func callRunAgent(ctx context.Context, task, model string) (bool, map[string]float64, error) {
	runAgentMu.Lock()
	defer runAgentMu.Unlock()
	return RunAgent(ctx, task, model)
}

// SetRunAgent swaps the RunAgent hook under runAgentMu. Test setup and
// teardown must use this rather than assigning RunAgent directly so the
// spawned goroutines in caseForTask never observe a half-written hook.
func SetRunAgent(h agentHook) {
	runAgentMu.Lock()
	defer runAgentMu.Unlock()
	RunAgent = h
}

func caseForTask(task, model string, cfg Config) func(ctx context.Context) evalfw.CaseResult {
	return func(ctx context.Context) evalfw.CaseResult {
		caseCtx, cancel := context.WithTimeout(ctx, cfg.LoopguardTimeout)
		defer cancel()

		type result struct {
			ok     bool
			metric map[string]float64
			err    error
		}
		ch := make(chan result, 1)
		go func() {
			ok, metric, err := callRunAgent(caseCtx, task, model)
			ch <- result{ok: ok, metric: metric, err: err}
		}()

		select {
		case <-caseCtx.Done():
			return evalfw.CaseResult{
				Verdict: evalfw.VerdictFail,
				Error:   LoopguardError.Error(),
				Metrics: map[string]float64{"loopguard_tripped": 1},
			}
		case r := <-ch:
			cr := evalfw.CaseResult{Metrics: map[string]float64{}}
			for k, v := range r.metric {
				cr.Metrics[k] = v
			}
			if r.err != nil {
				cr.Verdict = evalfw.VerdictFail
				cr.Error = r.err.Error()
				return cr
			}
			if !r.ok {
				cr.Verdict = evalfw.VerdictFail
				cr.Error = fmt.Sprintf("agent returned not-ok for task=%s model=%s", task, model)
				return cr
			}
			cr.Verdict = evalfw.VerdictPass
			cr.Metrics["model"] = hashModel(model)
			cr.Metrics["task"] = hashTask(task)
			return cr
		}
	}
}

// CheckCodeMapFreshness reads cfg.CodeMapPath and returns an ErrStaleCODEMAP
// when the file's mtime is older than cfg.LoopguardTimeout-style TTL.
// Returning nil means the CODEMAP is fresh enough to trust.
func CheckCodeMapFreshness(cfg Config) error {
	cfg = cfg.withDefaults()
	info, err := os.Stat(cfg.CodeMapPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrMissingCODEMAP, cfg.CodeMapPath)
		}
		return fmt.Errorf("agenteval: stat CODEMAP: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%w: %s is zero bytes", ErrMissingCODEMAP, cfg.CodeMapPath)
	}
	age := cfg.Now().Sub(info.ModTime())
	if age > FreshnessTTL {
		return &ErrStaleCODEMAP{Age: age, TTL: FreshnessTTL}
	}
	return nil
}

// CompatibleRubrics returns rubric.All names for cross-checking against
// the integration suite's tag set. Used by TestRubricCompatibility.
func CompatibleRubrics() []string {
	out := make([]string, 0, len(rubric.All))
	for _, r := range rubric.All {
		out = append(out, r.Name)
	}
	return out
}

// RunIntegrationSuite is the convenience wrapper that assembles the
// suite, runs it, and emits a per-row verdict to the harness report.
// Errors are returned only for setup failures; per-Case failures are
// reflected in the SuiteResult.
func RunIntegrationSuite(ctx context.Context, runID string, cfg Config) (*evalfw.SuiteResult, error) {
	suite, err := SuiteForRun(runID, cfg)
	if err != nil {
		return nil, fmt.Errorf("agenteval: assemble suite: %w", err)
	}
	runner := evalfw.NewRunner(evalfw.RunnerConfig{Timeout: cfg.LoopguardTimeout + time.Second})
	return runner.RunSuite(ctx, suite)
}

// hashTask + hashModel are tiny non-cryptographic hashes used only to
// populate deterministic metric values. They make the eval output
// diff-friendly without exposing the raw strings (model IDs can
// carry tenant context in some deployments).
func hashTask(s string) float64 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return float64(h % 1000)
}

func hashModel(s string) float64 {
	return float64(len(s))
}

// String returns a one-line summary of the suite for log output.
func String(s evalfw.Suite) string {
	var b strings.Builder
	fmt.Fprintf(&b, "suite=%s cases=%d", s.Name, len(s.Cases))
	return b.String()
}
