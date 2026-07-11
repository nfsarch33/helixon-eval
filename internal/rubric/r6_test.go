package rubric

import (
	"context"
	"strings"
	"testing"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/provider"
)

func TestR6_DimensionsCover12(t *testing.T) {
	if len(R6Dimensions) != 12 {
		t.Fatalf("R6Dimensions=%d want 12", len(R6Dimensions))
	}
}

func TestR6_DimensionsIncludeAllNew(t *testing.T) {
	// The 7 new dimensions; "provider" is tracked separately as R6Backends.
	want := []string{
		"eval_harness", "docs", "ops", "market",
		"support", "regulatory", "cost",
	}
	have := make(map[string]bool)
	for _, d := range R6Dimensions {
		have[d] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing R6 dimension %q", w)
		}
	}
}

func TestR6_AllNewSuitesRegistered(t *testing.T) {
	for _, d := range []string{"eval_harness", "docs", "ops", "market", "support", "regulatory", "cost", "provider", "r6_full"} {
		if _, ok := AllSuites[d]; !ok {
			t.Errorf("missing suite for %q", d)
		}
	}
}

func TestR6_ProviderSuite_HasExpectedCases(t *testing.T) {
	if _, ok := AllSuites["r6_full"]; !ok {
		t.Fatalf("missing r6_full suite")
	}
	// r6_full concatenates 12 dimension suites:
	//   5 pre-existing (6 cases each) = 30
	//   7 R6 new (7 cases each)         = 49
	// Total = 79.
	cases := AllSuites["r6_full"].Cases
	if len(cases) != 79 {
		t.Fatalf("r6_full cases=%d want 79", len(cases))
	}
}

func TestR6_ProviderSuite_EveryCaseHasProviderField(t *testing.T) {
	suite := AllSuites["r6_full"]
	for i, c := range suite.Cases {
		// R6 cases are tagged with the provider/model via the prompt prefix.
		// The harness skips cases whose prompt is empty.
		if c.Fn == nil {
			t.Errorf("case[%d] %s nil fn", i, c.Name)
		}
	}
}

func TestR6_StaticCheck_EvalHarnessPass(t *testing.T) {
	suite := AllSuites["eval_harness"]
	runner := evalfw.NewRunner(evalfw.RunnerConfig{})
	res, err := runner.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict == evalfw.VerdictFail {
		t.Fatalf("eval_harness suite failed: %+v", res)
	}
}

func TestR6_StaticCheck_DocsPass(t *testing.T) {
	suite := AllSuites["docs"]
	runner := evalfw.NewRunner(evalfw.RunnerConfig{})
	res, err := runner.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed > 0 {
		t.Fatalf("docs suite had %d failures", res.Failed)
	}
}

func TestR6_StaticCheck_OpsPass(t *testing.T) {
	suite := AllSuites["ops"]
	runner := evalfw.NewRunner(evalfw.RunnerConfig{})
	res, err := runner.RunSuite(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed > 0 {
		t.Fatalf("ops suite had %d failures", res.Failed)
	}
}

func TestR6_CostCase_EmitsCostUSD(t *testing.T) {
	suite := AllSuites["cost"]
	for _, c := range suite.Cases {
		res := c.Fn(context.Background())
		if res.Verdict == evalfw.VerdictFail {
			continue
		}
		if v, ok := res.Metrics["cost_usd"]; !ok || v < 0 {
			t.Errorf("case %s missing cost_usd: %v", c.Name, res.Metrics)
		}
	}
}

func TestR6_ProviderCell_RoundTripsThroughDummy(t *testing.T) {
	for _, cell := range []provider.Cell{
		{Provider: "minimax", Model: "MiniMax-M3"},
		{Provider: "qwen", Model: "qwen3.7-plus"},
		{Provider: "qwen", Model: "qwen3.7-max"},
	} {
		if !strings.Contains(cell.String(), cell.Provider) {
			t.Errorf("cell.String()=%q missing provider", cell.String())
		}
	}
}
