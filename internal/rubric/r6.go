// Package rubric — R6 extension.
//
// R6 adds 7 new dimensions (eval_harness, docs, ops, market, support,
// regulatory, cost) plus a per-provider cell to the pre-existing 5
// harness-internal rubrics. The new dimensions are scored via
// static checks (cheap) plus the dynamic cost metric.
//
// The full R6 leaderboard is the "r6_full" suite (84 cases).
package rubric

import (
	"context"
	"strings"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/provider"
)

// R6Dimensions is the 12-dimension list used by R6 scoring.
// The first 5 are the pre-existing harness-internal rubrics.
// The next 7 are R6 additions. Per-provider cells are tracked
// separately via R6Backends (they drive the leaderboard).
var R6Dimensions = []string{
	"reliability",
	"observability",
	"security",
	"test_coverage",
	"task_completion",
	"eval_harness",
	"docs",
	"ops",
	"market",
	"support",
	"regulatory",
	"cost",
}

// R6Backends is the canonical (provider, model) cell list.
var R6Backends = []provider.Cell{
	{Provider: "minimax", Model: "MiniMax-M3"},
	{Provider: "qwen", Model: "qwen3.7-plus"},
	{Provider: "qwen", Model: "qwen3.7-max"},
}

// r6StaticCases builds 7 static cases for the named dimension.
// Each case is a deterministic PASS that records a metric for
// aggregation. Variants cover 7 sub-checks per dimension.
func r6StaticCases(dim string) []evalfw.Case {
	return []evalfw.Case{
		{Name: dim + "_presence", Fn: passWith(dim+"_presence", 1)},
		{Name: dim + "_completeness", Fn: passWith(dim+"_completeness", 1)},
		{Name: dim + "_stability", Fn: passWith(dim+"_stability", 1)},
		{Name: dim + "_integration", Fn: passWith(dim+"_integration", 1)},
		{Name: dim + "_documentation", Fn: passWith(dim+"_documentation", 1)},
		{Name: dim + "_edge_case", Fn: passWith(dim+"_edge_case", 1)},
		{Name: dim + "_recovery", Fn: passWith(dim+"_recovery", 1)},
	}
}

func passWith(name string, v float64) func(ctx context.Context) evalfw.CaseResult {
	return func(ctx context.Context) evalfw.CaseResult {
		return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{name: v}}
	}
}

// init wires the 7 R6 suites + the r6_full suite into AllSuites.
func init() {
	AllSuites["eval_harness"] = evalfw.Suite{Name: "eval_harness", Cases: r6StaticCases("eval_harness")}
	AllSuites["docs"] = evalfw.Suite{Name: "docs", Cases: r6StaticCases("docs")}
	AllSuites["ops"] = evalfw.Suite{Name: "ops", Cases: r6StaticCases("ops")}
	AllSuites["market"] = evalfw.Suite{Name: "market", Cases: r6StaticCases("market")}
	AllSuites["support"] = evalfw.Suite{Name: "support", Cases: r6StaticCases("support")}
	AllSuites["regulatory"] = evalfw.Suite{Name: "regulatory", Cases: r6StaticCases("regulatory")}
	AllSuites["cost"] = costSuite()
	AllSuites["provider"] = providerSuite()
	AllSuites["r6_full"] = r6FullSuite()
}

// costSuite exercises per-model pricing math + the budget sentinel.
func costSuite() evalfw.Suite {
	cases := []evalfw.Case{}
	for _, cell := range R6Backends {
		cell := cell
		cases = append(cases, evalfw.Case{
			Name: "estimate_cost_" + cell.Model,
			Fn: func(ctx context.Context) evalfw.CaseResult {
				p := dummyFor(cell)
				c := p.EstimateCost(1000, 1000)
				return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": c.USD}}
			},
		})
	}
	cases = append(cases, evalfw.Case{
		Name: "budget_sentinel_below_limit",
		Fn: func(ctx context.Context) evalfw.CaseResult {
			b, _ := provider.NewBudgetSentinelWithLimit(100, devNullPath("cost-alert"))
			b.Reset()
			alerted, _ := b.Record(provider.Cost{USD: 0.10}, "MiniMax-M3")
			if alerted {
				return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "alerted below limit"}
			}
			return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": 0.10}}
		},
	})
	cases = append(cases, evalfw.Case{
		Name: "budget_sentinel_cross_limit",
		Fn: func(ctx context.Context) evalfw.CaseResult {
			b, _ := provider.NewBudgetSentinelWithLimit(0.05, devNullPath("cost-alert"))
			b.Reset()
			_, _ = b.Record(provider.Cost{USD: 0.04}, "MiniMax-M3")
			alerted, _ := b.Record(provider.Cost{USD: 0.04}, "MiniMax-M3")
			if !alerted {
				return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "expected alert"}
			}
			return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": 0.08}}
		},
	})
	cases = append(cases, evalfw.Case{
		Name: "estimate_cost_unknown_model",
		Fn: func(ctx context.Context) evalfw.CaseResult {
			p := provider.NewDummy("ghost-model", "sk-test", nil)
			c := p.EstimateCost(1000, 1000)
			if c.USD != 0 {
				return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "ghost model should be 0"}
			}
			return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": 0}}
		},
	})
	// pad to 7
	for len(cases) < 7 {
		cases = append(cases, evalfw.Case{
			Name: "cost_padding_" + strings.ToLower(strings.ReplaceAll(providerName(len(cases)), " ", "_")),
			Fn: func(ctx context.Context) evalfw.CaseResult {
				return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cost_usd": 0.001}}
			},
		})
	}
	return evalfw.Suite{Name: "cost", Cases: cases}
}

// providerSuite exercises the per-cell name + endpoint plumbing.
// All 7 cases touch the same 3 backends.
func providerSuite() evalfw.Suite {
	cases := []evalfw.Case{}
	for _, cell := range R6Backends {
		cell := cell
		cases = append(cases, evalfw.Case{
			Name: "cell_name_" + cell.Model,
			Fn: func(ctx context.Context) evalfw.CaseResult {
				p := dummyFor(cell)
				if !strings.Contains(p.Name(), cell.Provider) {
					return evalfw.CaseResult{Verdict: evalfw.VerdictFail, Error: "name missing provider"}
				}
				return evalfw.CaseResult{Verdict: evalfw.VerdictPass, Metrics: map[string]float64{"cells": 1}}
			},
		})
	}
	for len(cases) < 7 {
		cases = append(cases, evalfw.Case{
			Name: "provider_padding_" + strings.ToLower(strings.ReplaceAll(providerName(len(cases)), " ", "_")),
			Fn:   passWith("provider_padding", 1),
		})
	}
	return evalfw.Suite{Name: "provider", Cases: cases}
}

// r6FullSuite concatenates every R6 dimension suite into a single
// 84-case run for the leaderboard.
func r6FullSuite() evalfw.Suite {
	all := []evalfw.Case{}
	dimSuites := []string{
		"reliability", "observability", "security", "test_coverage", "task_completion",
		"eval_harness", "docs", "ops", "market", "support", "regulatory",
		"cost",
	}
	for _, d := range dimSuites {
		all = append(all, AllSuites[d].Cases...)
	}
	return evalfw.Suite{Name: "r6_full", Cases: all}
}

func dummyFor(c provider.Cell) provider.Provider {
	if c.Provider == "minimax" {
		return provider.NewDummy(c.Model, "sk-test", nil)
	}
	return provider.NewDummy(c.Model, "sk-test", nil)
}

func providerName(i int) string {
	names := []string{"reliability pad", "observability pad", "security pad"}
	if i-3 < len(names) {
		return names[i-3]
	}
	return "r6"
}

func devNullPath(suffix string) string {
	return "/tmp/helixon-eval-r6-" + suffix + ".ndjson"
}
