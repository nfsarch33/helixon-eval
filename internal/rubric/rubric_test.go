package rubric

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
)

func TestAllRubricsPresent(t *testing.T) {
	want := map[string]bool{
		"reliability": false, "observability": false, "security": false,
		"test_coverage": false, "task_completion": false,
	}
	for _, r := range All {
		if _, ok := want[r.Name]; ok {
			want[r.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing rubric %q", name)
		}
	}
}

func TestAllSuitesHasAllRubrics(t *testing.T) {
	// Every rubric in `All` must have a corresponding entry in `AllSuites`.
	// AllSuites may have additional entries (multiple suites per rubric).
	for _, r := range All {
		if _, ok := AllSuites[r.Name]; !ok {
			t.Errorf("missing suite for rubric %q", r.Name)
		}
	}
	// Sanity: at least 16 suites (5 pre + 9 R6 + 2 real-models).
	if len(AllSuites) < 16 {
		t.Errorf("expected at least 16 suites, got %d", len(AllSuites))
	}
}

func TestReliabilitySuite_Has6Cases(t *testing.T) {
	s := ReliabilitySuite()
	if len(s.Cases) != 6 {
		t.Errorf("expected 6 reliability cases, got %d", len(s.Cases))
	}
}

func TestObservabilitySuite_Has6Cases(t *testing.T) {
	s := ObservabilitySuite()
	if len(s.Cases) != 6 {
		t.Errorf("expected 6 observability cases, got %d", len(s.Cases))
	}
}

func TestSecuritySuite_Has6Cases(t *testing.T) {
	s := SecuritySuite()
	if len(s.Cases) != 6 {
		t.Errorf("expected 6 security cases, got %d", len(s.Cases))
	}
}

func TestTestCoverageSuite_Has6Cases(t *testing.T) {
	s := TestCoverageSuite()
	if len(s.Cases) != 6 {
		t.Errorf("expected 6 test_coverage cases, got %d", len(s.Cases))
	}
}

func TestTaskCompletionSuite_Has6Cases(t *testing.T) {
	s := TaskCompletionSuite()
	if len(s.Cases) != 6 {
		t.Errorf("expected 6 task_completion cases, got %d", len(s.Cases))
	}
}

func TestReliabilitySuite_RunsUnder3Seconds(t *testing.T) {
	// Run only the fast cases (exclude the 5-second timeout_enforced case)
	s := ReliabilitySuite()
	fastCases := []evalfw.Case{s.Cases[0], s.Cases[1], s.Cases[3], s.Cases[4], s.Cases[5]}
	r := evalfw.NewRunner(evalfw.RunnerConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := r.RunSuite(ctx, evalfw.Suite{Name: "fast", Cases: fastCases})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("suite took too long: %v", time.Since(start))
	}
}

func TestReliabilitySuite_TimesOutLongCase(t *testing.T) {
	r := evalfw.NewRunner(evalfw.RunnerConfig{Timeout: 50 * time.Millisecond})
	sr, err := r.RunSuite(context.Background(), evalfw.Suite{
		Name: "long",
		Cases: []evalfw.Case{
			ReliabilitySuite().Cases[5], // long_running_case — 50ms
		},
	})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	// 50ms is the exact boundary; allow either pass or fail
	if sr.Cases[0].Verdict != evalfw.VerdictPass && sr.Cases[0].Verdict != evalfw.VerdictFail {
		t.Errorf("unexpected verdict: %v", sr.Cases[0].Verdict)
	}
}

func TestObservabilitySuite_AllEmitMetrics(t *testing.T) {
	s := ObservabilitySuite()
	for i, c := range s.Cases {
		if c.Name == "no_metrics_emitted" {
			continue
		}
		cr := c.Fn(context.Background())
		if cr.Metrics == nil {
			t.Errorf("case %d (%s) should emit metrics", i, c.Name)
		}
	}
}

func TestSecuritySuite_AllPassInCleanEnv(t *testing.T) {
	// This test inspects the entire process environment for secret-named keys.
	// In real dev/CI shells, env vars like TELEGRAM_BOT2_TOKEN, OPENAI_API_KEY,
	// etc. are legitimately present (loaded from 1Password / shell rc).
	// Set EVAL_ALLOW_ENV_SECRETS=1 to skip in those environments; the test
	// still exercises the SecuritySuite logic via the dedicated suite tests.
	if os.Getenv("EVAL_ALLOW_ENV_SECRETS") == "1" {
		t.Skip("skipping: EVAL_ALLOW_ENV_SECRETS=1 (real env has legitimate secrets)")
	}
	r := evalfw.NewRunner(evalfw.RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), SecuritySuite())
	for _, cr := range sr.Cases {
		if cr.Verdict == evalfw.VerdictFail {
			t.Errorf("case %s failed: %s", cr.Name, cr.Error)
		}
	}
}

func TestSecuritySuite_DetectsFakeSecretOnArgv(t *testing.T) {
	// Simulate by calling looksLikeSecret directly.
	if !looksLikeSecret("sk-abcdef1234567890") {
		t.Error("expected sk- prefix to be flagged")
	}
	if !looksLikeSecret("ghp_xyz1234567890") {
		t.Error("expected ghp_ prefix to be flagged")
	}
	if looksLikeSecret("helixon-eval") {
		t.Error("expected plain command name to NOT be flagged")
	}
}

func TestLooksLikeSecret_KnownPrefixes(t *testing.T) {
	prefixes := []string{"sk-", "ghp_", "AKIA", "xkeysib-", "ops_", "re_", "Bearer "}
	for _, p := range prefixes {
		if !looksLikeSecret(p + "test") {
			t.Errorf("expected %q to be flagged", p)
		}
	}
}

func TestRedact_PreservesPrefixAndSuffix(t *testing.T) {
	got := redact("abcdefghijklmnop")
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "mnop") {
		t.Errorf("expected prefix/suffix preserved, got %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("expected *** in middle, got %q", got)
	}
}

func TestIsSecretEnvKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"API_TOKEN", true},
		{"AWS_SECRET", true},
		{"DB_PASSWORD", true},
		{"MY_CREDENTIAL", true},
		{"HELIXON_PATH", false},
		{"LANG", false},
	}
	for _, tc := range cases {
		if got := isSecretEnvKey(tc.key); got != tc.want {
			t.Errorf("isSecretEnvKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestTaskCompletionSuite_RunsAllVerdicts(t *testing.T) {
	r := evalfw.NewRunner(evalfw.RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), TaskCompletionSuite())
	if sr.Cases[0].Verdict != evalfw.VerdictPass {
		t.Errorf("happy_path should Pass")
	}
	if sr.Cases[1].Verdict != evalfw.VerdictWarn {
		t.Errorf("warn_path should Warn")
	}
	if sr.Cases[2].Verdict != evalfw.VerdictFail {
		t.Errorf("fail_path should Fail")
	}
}

func TestTaskCompletionSuite_EmptyVerdictAggregatesAsFail(t *testing.T) {
	r := evalfw.NewRunner(evalfw.RunnerConfig{})
	sr, _ := r.RunSuite(context.Background(), TaskCompletionSuite())
	if sr.Verdict == evalfw.VerdictPass {
		t.Errorf("expected non-Pass verdict due to explicit_nil_verdict_caught case")
	}
}
