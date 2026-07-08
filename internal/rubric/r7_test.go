package rubric

import (
	"context"
	"strings"
	"testing"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
)

// TestR7Dimensions_IncludesGuardrailCompliance verifies v16752-2 R5 retrofit:
// the new dimension is appended to R7Dimensions after R6Dimensions.
func TestR7Dimensions_IncludesGuardrailCompliance(t *testing.T) {
	if len(R7Dimensions) != len(R6Dimensions)+1 {
		t.Fatalf("R7Dimensions = %d; want %d (R6+1)", len(R7Dimensions), len(R6Dimensions)+1)
	}
	if R7Dimensions[len(R7Dimensions)-1] != "guardrail_compliance" {
		t.Errorf("last R7 dim = %q; want guardrail_compliance", R7Dimensions[len(R7Dimensions)-1])
	}
}

func TestGuardrailComplianceSuite_HasSevenCases(t *testing.T) {
	s := guardrailComplianceSuite()
	if s.Name != "guardrail_compliance" {
		t.Errorf("suite name = %q; want guardrail_compliance", s.Name)
	}
	if got := len(s.Cases); got != 7 {
		t.Errorf("suite len = %d; want 7", got)
	}
}

func TestGuardrailComplianceSuite_AllCasesPass(t *testing.T) {
	s := guardrailComplianceSuite()
	ctx := context.Background()
	for _, c := range s.Cases {
		res := c.Fn(ctx)
		if res.Verdict != evalfw.VerdictPass {
			t.Errorf("case %q verdict = %v; want PASS (err=%v)", c.Name, res.Verdict, res.Error)
		}
		if res.Metrics == nil {
			t.Errorf("case %q metrics = nil; want non-nil", c.Name)
		}
		if !strings.HasPrefix(c.Name, "guardrail_compliance_") {
			t.Errorf("case %q does not start with guardrail_compliance_", c.Name)
		}
	}
}

func TestR7FullSuite_IncludesAllDimensions(t *testing.T) {
	s := r7FullSuite()
	if s.Name != "r7_full" {
		t.Errorf("suite name = %q; want r7_full", s.Name)
	}
	if got := len(s.Cases); got < 84 {
		t.Errorf("r7_full cases = %d; want >= 84", got)
	}
}

func TestGuardrailCheckSubcases(t *testing.T) {
	cases := []struct {
		name string
		fn   func(context.Context) evalfw.CaseResult
	}{
		{"presence", guardrailCheckPresence},
		{"completeness", guardrailCheckCompleteness},
		{"stability", guardrailCheckStability},
		{"integration", guardrailCheckIntegration},
		{"documentation", guardrailCheckDocumentation},
		{"edge_case", guardrailCheckEdgeCase},
		{"recovery", guardrailCheckRecovery},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.fn(ctx)
			if res.Verdict != evalfw.VerdictPass {
				t.Errorf("%s verdict = %v; want PASS", tc.name, res.Verdict)
			}
			if res.Error == "" {
				t.Errorf("%s Error empty; want non-empty rationale", tc.name)
			}
		})
	}
}

func TestAllSuites_ContainsGuardrailCompliance(t *testing.T) {
	s, ok := AllSuites["guardrail_compliance"]
	if !ok {
		t.Fatal("AllSuites missing guardrail_compliance")
	}
	if s.Name != "guardrail_compliance" {
		t.Errorf("AllSuites[guardrail_compliance].Name = %q; want guardrail_compliance", s.Name)
	}
	if _, ok := AllSuites["r7_full"]; !ok {
		t.Fatal("AllSuites missing r7_full")
	}
}
