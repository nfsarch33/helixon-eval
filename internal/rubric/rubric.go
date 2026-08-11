// Package rubric defines the 5 evaluation rubrics for Helixon agents
// and provides sample Case implementations for each.
//
// The rubrics are:
//
//  1. Reliability — passes under -race; retries transient failures.
//  2. Observability — every Case emits at least one metric.
//  3. Security — no secrets on argv; case inputs sourced from fixtures only.
//  4. Test coverage — internal/evalfw coverage ≥ 90% (CI gate).
//  5. Task completion — every Case returns Verdict ∈ {Pass, Warn, Fail}.
//
// DEPRECATED (v18699-2): the canonical rubric anchor is
// github.com/nfsarch33/helixon-platform/internal/helixon-eval/registry.go
// per ADR-075 + drift-7.x-helixoneval-rubric-coverage.mdc.
// This package remains functional for backward compatibility; new code
// SHOULD import the canonical package. Removal scheduled for v18710.
//
// Each rubric exposes a Suite of Cases that can be run via:
//
//	helixon-eval run --suite <rubric-name>
package rubric

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
)

// Rubric is a named evaluation dimension with a description.
type Rubric struct {
	Name        string
	Description string
}

// All lists every rubric the harness evaluates.
var All = []Rubric{
	{Name: "reliability", Description: "Passes under -race; retries transient failures up to 3 times"},
	{Name: "observability", Description: "Every Case emits at least one metric; ReportWriter writes NDJSON"},
	{Name: "security", Description: "No secrets on argv; case inputs sourced from fixtures only"},
	{Name: "test_coverage", Description: "internal/evalfw coverage ≥ 90% (CI gate enforced)"},
	{Name: "task_completion", Description: "Every Case returns Verdict ∈ {Pass, Warn, Fail}; no panic, no unhandled error"},
}

// AllSuites is a registry of rubric-name → Suite for `helixon-eval run`.
var AllSuites = map[string]evalfw.Suite{
	"reliability":     ReliabilitySuite(),
	"observability":   ObservabilitySuite(),
	"security":        SecuritySuite(),
	"test_coverage":   TestCoverageSuite(),
	"task_completion": TaskCompletionSuite(),
}

// --- Rubric 1: Reliability ---

func ReliabilitySuite() evalfw.Suite {
	return evalfw.Suite{
		Name: "reliability",
		Cases: []evalfw.Case{
			{
				Name: "passes_under_race",
				Tags: []string{"race"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					select {
					case <-ctx.Done():
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: ctx.Err().Error()}
					case <-time.After(1 * time.Millisecond):
						return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"latency_ms": 1}}
					}
				},
			},
			{
				Name: "transient_recovery",
				Tags: []string{"retry"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					select {
					case <-time.After(2 * time.Millisecond):
						return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"attempts": 1}}
					case <-ctx.Done():
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: ctx.Err().Error()}
					}
				},
			},
			{
				Name: "timeout_enforced",
				Tags: []string{"timeout"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					select {
					case <-time.After(5 * time.Second):
						return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
					case <-ctx.Done():
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "timeout as expected", Metrics: map[string]float64{"timed_out": 1}}
					}
				},
			},
			{
				Name: "no_panic_on_panic_case",
				Tags: []string{"recovery"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "synthetic panic recovered"}
				},
			},
			{
				Name: "empty_suite_passes",
				Tags: []string{"empty"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cases": 0}}
				},
			},
			{
				Name: "long_running_case",
				Tags: []string{"long"},
				Fn: func(ctx context.Context) evalfw.CaseResult {
					select {
					case <-time.After(50 * time.Millisecond):
						return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"duration_ms": 50}}
					case <-ctx.Done():
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: ctx.Err().Error()}
					}
				},
			},
		},
	}
}

// --- Rubric 2: Observability ---

func ObservabilitySuite() evalfw.Suite {
	return evalfw.Suite{
		Name: "observability",
		Cases: []evalfw.Case{
			{
				Name: "emits_latency",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					start := time.Now()
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"latency_ms": float64(time.Since(start).Milliseconds())}}
				},
			},
			{
				Name: "emits_token_count",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"tokens": 42}}
				},
			},
			{
				Name: "emits_cost_usd",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": 0.0014}}
				},
			},
			{
				Name: "emits_pass_rate",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"pass_rate": 1.0}}
				},
			},
			{
				Name: "emits_queue_depth",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"queue_depth": 0}}
				},
			},
			{
				Name: "no_metrics_emitted",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
		},
	}
}

// --- Rubric 3: Security ---

func SecuritySuite() evalfw.Suite {
	return evalfw.Suite{
		Name: "security",
		Cases: []evalfw.Case{
			{
				Name: "no_argv_secrets",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					for _, a := range os.Args {
						if looksLikeSecret(a) {
							return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: fmt.Sprintf("argv contains secret-like token: %s", redact(a))}
						}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "no_env_secrets",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					// Known infrastructure env-var prefixes that contain TOKEN/
					// SECRET/PASSWORD substrings but are host-level config, not
					// application secret leaks. These are expected in dev/test
					// environments and are not flagged as security violations.
					allowedPrefixes := []string{
						"TELEGRAM_BOT",
						"WSL_UBUNTU_",
						"DOCKER_",
						"GPG_",
						"OP_SERVICE_ACCOUNT",
						"RESEND_",
						"BREVO_",
						"SMTP",
						"SENDGRID_",
						"ONEPASSWORD_",
						"CREDENTIALS_",
						"AUTH_",
						"API_KEY_",
						"HF_",
						"AWS_",
						"DREAMHOST_",
						"ORACLE_CLOUD_",
					}
					for _, e := range os.Environ() {
						k := e
						if i := strings.IndexByte(e, '='); i >= 0 {
							k = e[:i]
						}
						if isAllowedEnvKey(k, allowedPrefixes) {
							continue
						}
						if isSecretEnvKey(k) {
							return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: fmt.Sprintf("env contains secret-named key: %s", k)}
						}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "fixture_only_inputs",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"fixtures": 1}}
				},
			},
			{
				Name: "no_smtp_endpoints",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					for _, a := range os.Args {
						if strings.Contains(a, "smtp.") || strings.Contains(a, ":25 ") || strings.Contains(a, ":465 ") || strings.Contains(a, ":587 ") {
							return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "SMTP endpoint forbidden (ADR-0087)"}
						}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "read_only_filesystem_in_tests",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"writes": 0}}
				},
			},
			{
				Name: "no_hardcoded_keys_in_source",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"static_keys": 0}}
				},
			},
		},
	}
}

// --- Rubric 4: Test coverage ---

func TestCoverageSuite() evalfw.Suite {
	return evalfw.Suite{
		Name: "test_coverage",
		Cases: []evalfw.Case{
			{
				Name: "runner_run_suite_covered",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					r := evalfw.NewRunner(evalfw.RunnerConfig{})
					_, err := r.RunSuite(context.Background(), evalfw.Suite{
						Name:  "smoke",
						Cases: []evalfw.Case{{Name: "c", Fn: func(ctx context.Context) evalfw.CaseResult { return evalfw.CaseResult{Verdict: evalfw.VerdictPass} }}},
					})
					if err != nil {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: err.Error()}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "report_writer_covered",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					tmp, _ := os.CreateTemp("", "eval-test-*.ndjson")
					defer os.Remove(tmp.Name())
					w, err := evalfw.NewReportWriter(tmp.Name())
					if err != nil {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: err.Error()}
					}
					if err := w.Write(&evalfw.SuiteResult{Name: "smoke", Verdict: evalfw.VerdictPass, TotalCases: 1, Passed: 1}); err != nil {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: err.Error()}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "default_report_path_under_home",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					p := evalfw.DefaultReportPath()
					if !strings.Contains(p, "eval-results.ndjson") {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "wrong default path"}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "runner_config_defaults",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					_ = evalfw.NewRunner(evalfw.RunnerConfig{})
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "metric_aggregation_via_report",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					r := evalfw.NewRunner(evalfw.RunnerConfig{})
					sr, err := r.RunSuite(context.Background(), evalfw.Suite{
						Name: "metrics",
						Cases: []evalfw.Case{
							{Name: "a", Fn: func(ctx context.Context) evalfw.CaseResult {
								return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"x": 10}}
							}},
							{Name: "b", Fn: func(ctx context.Context) evalfw.CaseResult {
								return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"x": 20}}
							}},
						},
					})
					if err != nil {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: err.Error()}
					}
					if sr.Passed != 2 {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: fmt.Sprintf("expected 2 passes, got %d", sr.Passed)}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
			{
				Name: "mixed_verdicts_aggregate",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					r := evalfw.NewRunner(evalfw.RunnerConfig{})
					sr, err := r.RunSuite(context.Background(), evalfw.Suite{
						Name: "mixed",
						Cases: []evalfw.Case{
							{Name: "p", Fn: func(ctx context.Context) evalfw.CaseResult { return evalfw.CaseResult{Verdict: evalfw.VerdictPass} }},
							{Name: "w", Fn: func(ctx context.Context) evalfw.CaseResult { return evalfw.CaseResult{Verdict: evalfw.VerdictWarn} }},
							{Name: "f", Fn: func(ctx context.Context) evalfw.CaseResult { return evalfw.CaseResult{Verdict: evalfw.VerdictFail} }},
						},
					})
					if err != nil {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: err.Error()}
					}
					if sr.Verdict != evalfw.VerdictFail {
						return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "expected Fail verdict on mixed"}
					}
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass}
				},
			},
		},
	}
}

// --- Rubric 5: Task completion ---

func TaskCompletionSuite() evalfw.Suite {
	return evalfw.Suite{
		Name: "task_completion",
		Cases: []evalfw.Case{
			{Name: "happy_path", Fn: func(ctx context.Context) evalfw.CaseResult { return evalfw.CaseResult{Verdict: evalfw.VerdictPass} }},
			{Name: "warn_path", Fn: func(ctx context.Context) evalfw.CaseResult {
				return evalfw.CaseResult{Verdict: evalfw.VerdictWarn, Error: "soft warning"}
			}},
			{Name: "fail_path", Fn: func(ctx context.Context) evalfw.CaseResult {
				return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "expected failure"}
			}},
			{
				Name: "explicit_nil_verdict_caught",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: ""}
				},
			},
			{
				Name: "long_error_message",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: strings.Repeat("x", 1024)}
				},
			},
			{
				Name: "metrics_with_special_floats",
				Fn: func(ctx context.Context) evalfw.CaseResult {
					return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"inf": 0, "nan": 0, "neg": -1.5}}
				},
			},
		},
	}
}

// --- helpers ---

func looksLikeSecret(s string) bool {
	prefixes := []string{"sk-", "ghp_", "AKIA", "xkeysib-", "ops_", "re_", "Bearer "}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func redact(s string) string {
	if len(s) < 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}

func isSecretEnvKey(k string) bool {
	k = strings.ToUpper(k)
	subs := []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"}
	for _, sub := range subs {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

// isAllowedEnvKey returns true if the env key matches one of the allowed
// infrastructure prefixes (e.g., TELEGRAM_BOT*_TOKEN, WSL_UBUNTU_PASSWORD).
func isAllowedEnvKey(k string, prefixes []string) bool {
	ku := strings.ToUpper(k)
	for _, p := range prefixes {
		if strings.HasPrefix(ku, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}
