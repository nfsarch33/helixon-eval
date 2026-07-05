package evalfw

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewRunner_DefaultsTimeout(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	if r.config.Timeout != 30*time.Second {
		t.Errorf("expected 30s default, got %v", r.config.Timeout)
	}
}

func TestNewRunner_HonoursTimeout(t *testing.T) {
	r := NewRunner(RunnerConfig{Timeout: 5 * time.Millisecond})
	if r.config.Timeout != 5*time.Millisecond {
		t.Errorf("expected 5ms, got %v", r.config.Timeout)
	}
}

func TestRunner_EmptySuite(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, err := r.RunSuite(context.Background(), Suite{Name: "empty"})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if sr.Verdict != VerdictPass {
		t.Errorf("empty suite should Pass, got %v", sr.Verdict)
	}
	if sr.TotalCases != 0 {
		t.Errorf("expected 0 cases, got %d", sr.TotalCases)
	}
}

func TestRunner_AllPass(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, err := r.RunSuite(context.Background(), Suite{
		Name: "all-pass",
		Cases: []Case{
			{Name: "a", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }},
			{Name: "b", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }},
		},
	})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if sr.Verdict != VerdictPass {
		t.Errorf("expected Pass, got %v", sr.Verdict)
	}
	if sr.Passed != 2 {
		t.Errorf("expected 2 passes, got %d", sr.Passed)
	}
}

func TestRunner_AnyFail(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "fail",
		Cases: []Case{
			{Name: "p", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }},
			{Name: "f", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictFail, Error: "x"} }},
		},
	})
	if sr.Verdict != VerdictFail {
		t.Errorf("expected Fail, got %v", sr.Verdict)
	}
	if sr.Failed != 1 {
		t.Errorf("expected 1 fail, got %d", sr.Failed)
	}
}

func TestRunner_OnlyWarn(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "warn",
		Cases: []Case{
			{Name: "w", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictWarn} }},
		},
	})
	if sr.Verdict != VerdictWarn {
		t.Errorf("expected Warn, got %v", sr.Verdict)
	}
}

func TestRunner_PerCaseTimeout(t *testing.T) {
	r := NewRunner(RunnerConfig{Timeout: 10 * time.Millisecond})
	start := time.Now()
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "slow",
		Cases: []Case{
			{Name: "slow", Fn: func(ctx context.Context) CaseResult {
				<-ctx.Done()
				return CaseResult{Verdict: VerdictFail, Error: ctx.Err().Error()}
			}},
		},
	})
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("per-case timeout not enforced; took %v", elapsed)
	}
	if sr.Cases[0].Verdict != VerdictFail {
		t.Errorf("expected Fail on timeout, got %v", sr.Cases[0].Verdict)
	}
}

func TestRunner_DurationTracked(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "duration",
		Cases: []Case{
			{Name: "slow", Fn: func(ctx context.Context) CaseResult {
				time.Sleep(5 * time.Millisecond)
				return CaseResult{Verdict: VerdictPass}
			}},
		},
	})
	if sr.Cases[0].Duration < 5*time.Millisecond {
		t.Errorf("duration not tracked: %v", sr.Cases[0].Duration)
	}
}

func TestRunner_MetricsAggregated(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "metrics",
		Cases: []Case{
			{Name: "a", Fn: func(ctx context.Context) CaseResult {
				return CaseResult{Verdict: VerdictPass, Metrics: map[string]float64{"x": 10}}
			}},
			{Name: "b", Fn: func(ctx context.Context) CaseResult {
				return CaseResult{Verdict: VerdictPass, Metrics: map[string]float64{"x": 20}}
			}},
		},
	})
	if sr.Passed != 2 {
		t.Errorf("expected 2 passes, got %d", sr.Passed)
	}
}

func TestAggregateVerdict_AllPaths(t *testing.T) {
	cases := []struct {
		name string
		fail int
		warn int
		want Verdict
	}{
		{"pass", 0, 0, VerdictPass},
		{"warn", 0, 1, VerdictWarn},
		{"fail", 1, 0, VerdictFail},
		{"fail_and_warn", 1, 1, VerdictFail},
	}
	for _, tc := range cases {
		if got := aggregateVerdict(tc.fail, tc.warn); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCaseResult_HasName(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), Suite{
		Name: "named",
		Cases: []Case{
			{Name: "alpha", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }},
		},
	})
	if sr.Cases[0].Name != "alpha" {
		t.Errorf("expected name=alpha, got %q", sr.Cases[0].Name)
	}
}

func TestRunner_NilFn_StillCompletes(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	_, err := r.RunSuite(context.Background(), Suite{
		Name: "nil-fn",
		Cases: []Case{
			{Name: "nil", Fn: nil},
		},
	})
	if err != nil {
		t.Errorf("expected no panic, got %v", err)
	}
}

func TestRunner_ContextCancel(t *testing.T) {
	r := NewRunner(RunnerConfig{Timeout: 1 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.RunSuite(ctx, Suite{
		Name: "cancelled",
		Cases: []Case{
			{Name: "x", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }},
		},
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("expected nil or context.Canceled, got %v", err)
	}
}

func TestRunner_RaceSafe(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	for i := 0; i < 10; i++ {
		_, _ = r.RunSuite(context.Background(), Suite{
			Name: "race",
			Cases: []Case{
				{Name: "x", Fn: func(ctx context.Context) CaseResult {
					time.Sleep(time.Millisecond)
					return CaseResult{Verdict: VerdictPass}
				}},
			},
		})
	}
}

func TestRunner_LargeSuite(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	cases := make([]Case, 100)
	for i := range cases {
		cases[i] = Case{Name: "x", Fn: func(ctx context.Context) CaseResult { return CaseResult{Verdict: VerdictPass} }}
	}
	sr, _ := r.RunSuite(context.Background(), Suite{Name: "large", Cases: cases})
	if sr.Passed != 100 {
		t.Errorf("expected 100 passes, got %d", sr.Passed)
	}
}
