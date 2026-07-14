package agenteval_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/agenteval"
	"github.com/nfsarch33/helixon-eval/internal/evalfw"
)

// Test1_5x3ModelMatrix_Passes asserts the cross product is exactly
// 5 tasks × 3 models = 15 cases. A drift here means the canonical
// task set or production model list changed without updating both the
// integration layer and the eval matrix assertions.
func Test1_5x3ModelMatrix_Passes(t *testing.T) {
	suite, err := agenteval.SuiteForRun("v18628-1", agenteval.Config{})
	if err != nil {
		t.Fatalf("SuiteForRun: %v", err)
	}
	if got, want := len(suite.Cases), 15; got != want {
		t.Fatalf("expected %d (5 tasks × 3 models) cases, got %d", want, got)
	}
	// Every (task, model) pair must appear exactly once.
	for _, task := range agenteval.CanonicalTasks {
		for _, model := range agenteval.ProductionModels {
			name := task + "__" + model
			found := false
			for _, c := range suite.Cases {
				if c.Name == name {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("missing case %q in 5×3 matrix", name)
			}
		}
	}
}

// Test2_RubricCompatibility_AllRubricsCovered ensures the agent eval
// reuses rubric.All (the v16129 invariant) so any future rubric added
// flows automatically into the agent eval surface.
func Test2_RubricCompatibility_AllRubricsCovered(t *testing.T) {
	names := agenteval.CompatibleRubrics()
	want := map[string]bool{
		"reliability": false, "observability": false, "security": false,
		"test_coverage": false, "task_completion": false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for r, present := range want {
		if !present {
			t.Errorf("rubric %q missing from agenteval.CompatibleRubrics()", r)
		}
	}
}

// Test3_CodeMapFreshness_PassForRecentFile writes a synthetic CODEMAP
// with mtime = now-1h; CheckCodeMapFreshness must return nil.
func Test3_CodeMapFreshness_PassForRecentFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "CODEMAP.md")
	if err := os.WriteFile(p, []byte("# helixon-platform surface\n\n- agent/checkpoint\n- agent/livechannel\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(p, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := agenteval.CheckCodeMapFreshness(agenteval.Config{CodeMapPath: p}); err != nil {
		t.Fatalf("expected nil for fresh CODEMAP, got %v", err)
	}
}

// Test3_CodeMapFreshness_FailForStaleFile pushes mtime 60 days into the
// past; the freshness TTL is 30 days, so the call must return an
// ErrStaleCODEMAP with the right Age and TTL fields populated.
func Test3_CodeMapFreshness_FailForStaleFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "CODEMAP.md")
	if err := os.WriteFile(p, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	stale := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(p, stale, stale); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	err := agenteval.CheckCodeMapFreshness(agenteval.Config{CodeMapPath: p})
	if err == nil {
		t.Fatal("expected ErrStaleCODEMAP for 60d-old file, got nil")
	}
	var staleErr *agenteval.ErrStaleCODEMAP
	if !errors.As(err, &staleErr) {
		t.Fatalf("expected *ErrStaleCODEMAP, got %T (%v)", err, err)
	}
	if staleErr.TTL != agenteval.FreshnessTTL {
		t.Errorf("TTL: got %s, want %s", staleErr.TTL, agenteval.FreshnessTTL)
	}
	if staleErr.Age <= 0 {
		t.Errorf("Age must be > 0, got %s", staleErr.Age)
	}
}

// Test3_CodeMapFreshness_FailWhenMissing proves a missing CODEMAP is
// a setup error, not a stale-file error — the operator must be told
// the file is absent, not that it is old.
func Test3_CodeMapFreshness_FailWhenMissing(t *testing.T) {
	err := agenteval.CheckCodeMapFreshness(agenteval.Config{
		CodeMapPath: filepath.Join(t.TempDir(), "does-not-exist.md"),
	})
	if err == nil {
		t.Fatal("expected error for missing CODEMAP, got nil")
	}
	if !errors.Is(err, agenteval.ErrMissingCODEMAP) {
		t.Fatalf("expected ErrMissingCODEMAP, got %T (%v)", err, err)
	}
}

// Test4_Loopguard_TripsOnLongCase installs a RunAgent hook that blocks
// until the case context fires; the loop guard must classify the case
// as FAIL with the loopguard_tripped metric and the LoopguardError
// string in the verdict.
func Test4_Loopguard_TripsOnLongCase(t *testing.T) {
	prev := agenteval.RunAgent
	t.Cleanup(func() { agenteval.RunAgent = prev })

	agenteval.RunAgent = func(ctx context.Context, task, model string) (bool, map[string]float64, error) {
		<-ctx.Done()
		return false, nil, ctx.Err()
	}

	suite, err := agenteval.SuiteForRun("loopguard-trip", agenteval.Config{LoopguardTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("SuiteForRun: %v", err)
	}
	result, err := evalfw.NewRunner(evalfw.RunnerConfig{Timeout: 2 * time.Second}).RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if result.Verdict != evalfw.VerdictFail {
		t.Fatalf("expected suite Verdict=Fail, got %s (passed=%d failed=%d)", result.Verdict, result.Passed, result.Failed)
	}
	if result.Failed != 15 {
		t.Errorf("expected all 15 cases to trip the loopguard, got failed=%d", result.Failed)
	}
	// Spot-check one case to confirm the metric + error are set.
	var first *evalfw.CaseResult
	for i := range result.Cases {
		if result.Cases[i].Metrics["loopguard_tripped"] == 1 {
			first = &result.Cases[i]
			break
		}
	}
	if first == nil {
		t.Fatal("no case recorded loopguard_tripped=1")
	}
	if first.Verdict != evalfw.VerdictFail {
		t.Errorf("loopguard case verdict: got %s, want Fail", first.Verdict)
	}
}

// Test5_IntegrationSuite_HappyPath runs the full 5×3 matrix with the
// default (passing) agent hook and asserts every case passes. This is
// the contract test the production pilot invokes before declaring
// "agent eval matrix GREEN".
func Test5_IntegrationSuite_HappyPath(t *testing.T) {
	prev := agenteval.RunAgent
	t.Cleanup(func() { agenteval.RunAgent = prev })

	agenteval.RunAgent = func(ctx context.Context, task, model string) (bool, map[string]float64, error) {
		return true, map[string]float64{"ok": 1, "task": float64(len(task))}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := agenteval.RunIntegrationSuite(ctx, "happy-path", agenteval.Config{})
	if err != nil {
		t.Fatalf("RunIntegrationSuite: %v", err)
	}
	if res.Verdict != evalfw.VerdictPass {
		t.Fatalf("expected Pass verdict, got %s (passed=%d failed=%d)", res.Verdict, res.Passed, res.Failed)
	}
	if res.TotalCases != 15 {
		t.Errorf("expected 15 cases, got %d", res.TotalCases)
	}
}
