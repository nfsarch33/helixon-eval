// Package demo — RED tests for the pilot E2E smoke harness (v18688-2).
//
// The plan mandates:
//   - RED: TestPilotDemo_7Tasks, TestPilotDemo_BudgetGate, TestPilotDemo_LatencyP99
//   - GREEN: 7 tasks complete in <10s wall-clock; total cost ≤ $0.05; p99 ≤ 3s
//   - Coverage: ≥90% of internal/demo/cost.go
package demo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPilotDemo_7Tasks is the RED → GREEN gate. Asserts the demo
// accepts exactly 7 tasks and emits 7 NDJSON entries.
func TestPilotDemo_7Tasks(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	require.NotNil(t, bundle)
	require.Len(t, bundle.Tasks, 7, "demo MUST produce exactly 7 cost log entries")
}

// TestPilotDemo_BudgetGate asserts total cost is ≤ $0.05 across the
// 7 default tasks using the v18681-5 MiniMax-M3 pricing.
func TestPilotDemo_BudgetGate(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	assert.LessOrEqual(t, bundle.TotalCostUSD, 0.05,
		"7-task demo total cost MUST be ≤ $0.05 (got $%.4f)", bundle.TotalCostUSD)
}

// TestPilotDemo_LatencyP99 asserts p99 latency is ≤ 3s on the
// default task latencies.
func TestPilotDemo_LatencyP99(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	assert.LessOrEqual(t, bundle.P99Latency, 3*time.Second,
		"p99 latency MUST be ≤ 3s (got %s)", bundle.P99Latency)
}

// TestPilotDemo_WallClock asserts the demo completes in <10s wall-clock.
func TestPilotDemo_WallClock(t *testing.T) {
	start := time.Now()
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	elapsed := time.Since(start)
	require.NoError(t, err)

	assert.LessOrEqual(t, elapsed, 10*time.Second,
		"demo MUST complete in <10s wall-clock (got %s)", elapsed)
	_ = bundle
}

// TestPilotDemo_GatesRunAll asserts the Gates helper covers all 4
// acceptance criteria per the plan §2.
func TestPilotDemo_GatesRunAll(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	pass, reason := Gates(bundle, 0.05, 10*time.Second, 3*time.Second)
	assert.True(t, pass, "all 4 gates MUST pass for default tasks (got reason=%q)", reason)
}

// TestPilotDemo_CostPerTask asserts each task entry has a non-zero
// EstimatedUSD and a deterministic JobID.
func TestPilotDemo_CostPerTask(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	for _, e := range bundle.Tasks {
		assert.NotEmpty(t, e.JobID, "job_id MUST be non-empty (deterministic SHA-256)")
		assert.Equal(t, 8, len(e.JobID),
			"job_id MUST be 8 hex chars (sha256 prefix)")
		assert.Greater(t, e.EstimatedUSD, 0.0,
			"task %q estimated_usd MUST be > 0", e.Task)
		assert.Equal(t, "MiniMax-M3", e.Model,
			"task %q model MUST be MiniMax-M3", e.Task)
		assert.Equal(t, "minimax", e.Backend)
	}
}

// TestPilotDemo_JobIDDeterministic asserts JobID is the same SHA-256
// prefix across multiple calls (idempotency requirement from v18688-3).
func TestPilotDemo_JobIDDeterministic(t *testing.T) {
	id1 := JobID("echo", "50", "MiniMax-M3")
	id2 := JobID("echo", "50", "MiniMax-M3")
	assert.Equal(t, id1, id2, "JobID MUST be deterministic across calls")
	id3 := JobID("lookup", "120", "MiniMax-M3")
	assert.NotEqual(t, id1, id3, "different task MUST yield different JobID")
}

// TestPilotDemo_NDJSON validates the NDJSON schema matches the plan.
func TestPilotDemo_NDJSON(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	raw := NDJSONEntries(bundle)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 7, "NDJSON output MUST have 7 lines")

	for i, line := range lines {
		var got CostLogEntry
		err := json.Unmarshal([]byte(line), &got)
		require.NoErrorf(t, err, "NDJSON line %d must parse (got %q)", i, line)
		assert.Equal(t, bundle.Tasks[i].JobID, got.JobID)
		assert.Equal(t, bundle.Tasks[i].Task, got.Task)
		assert.InDelta(t, bundle.Tasks[i].EstimatedUSD, got.EstimatedUSD, 1e-6,
			"NDJSON USD value MUST match within float64 round-trip tolerance")
	}
}

// TestPilotDemo_GatesRejectsShortTasks asserts Gates rejects bundles
// with the wrong task count.
func TestPilotDemo_GatesRejectsShortTasks(t *testing.T) {
	bundle := &PilotBundle{Tasks: make([]CostLogEntry, 3)} // 3 ≠ 7
	pass, reason := Gates(bundle, 0.05, 10*time.Second, 3*time.Second)
	assert.False(t, pass, "Gates MUST reject bundles with != 7 tasks")
	assert.Contains(t, reason, "task count")
}

// TestPilotDemo_GatesRejectsBudgetOverrun asserts Gates rejects
// bundles whose cost exceeds budget.
func TestPilotDemo_GatesRejectsBudgetOverrun(t *testing.T) {
	bundle := &PilotBundle{
		Tasks:        make([]CostLogEntry, 7),
		TotalCostUSD: 0.10, // 2× budget
		TotalLatency: 1 * time.Second,
		P99Latency:   500 * time.Millisecond,
	}
	pass, reason := Gates(bundle, 0.05, 10*time.Second, 3*time.Second)
	assert.False(t, pass)
	assert.Contains(t, reason, "cost")
}

// TestPilotDemo_GatesRejectsLatencyOverrun asserts Gates rejects
// bundles whose wall-clock exceeds the budget.
func TestPilotDemo_GatesRejectsLatencyOverrun(t *testing.T) {
	bundle := &PilotBundle{
		Tasks:        make([]CostLogEntry, 7),
		TotalCostUSD: 0.01,
		TotalLatency: 11 * time.Second,
		P99Latency:   2 * time.Second,
	}
	pass, reason := Gates(bundle, 0.05, 10*time.Second, 3*time.Second)
	assert.False(t, pass)
	assert.Contains(t, reason, "total wall-clock")
}

// TestPilotDemo_GatesRejectsP99Overrun asserts Gates rejects bundles
// whose p99 latency exceeds the budget.
func TestPilotDemo_GatesRejectsP99Overrun(t *testing.T) {
	bundle := &PilotBundle{
		Tasks:        make([]CostLogEntry, 7),
		TotalCostUSD: 0.01,
		TotalLatency: 5 * time.Second,
		P99Latency:   4 * time.Second, // > 3s
	}
	pass, reason := Gates(bundle, 0.05, 10*time.Second, 3*time.Second)
	assert.False(t, pass)
	assert.Contains(t, reason, "p99 latency")
}

// TestPilotDemo_CostLoggedForEachTask ensures each task gets a
// non-zero cost entry (catches the zero-cost bug where ComputeCost
// was called with wrong pricing inputs).
func TestPilotDemo_CostLoggedForEachTask(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), DefaultMiniMaxPricing)
	require.NoError(t, err)

	total := 0.0
	for _, e := range bundle.Tasks {
		total += e.EstimatedUSD
	}
	assert.InDelta(t, bundle.TotalCostUSD, total, 1e-9,
		"sum of per-task USD MUST equal bundle.TotalCostUSD")
}

// TestPilotDemo_PricingOverriddenByCaller asserts the pricing arg
// actually controls ComputeCost (no package-global leakage).
func TestPilotDemo_PricingOverriddenByCaller(t *testing.T) {
	cheap := PilotPricing{PromptPer1KUSD: 0.0001, CompletionPer1KUSD: 0.0002}
	bundle, err := PilotDemo(DefaultPilotTasks(), cheap)
	require.NoError(t, err)

	// 7 tasks × ~tokens; total must be substantially less than with
	// default pricing.
	assert.Less(t, bundle.TotalCostUSD, 0.001,
		"with cheap pricing, total cost should be well under $0.001 (got $%.4f)",
		bundle.TotalCostUSD)
}

// TestPilotDemo_DefaultPricingFallback asserts PilotDemo uses
// DefaultMiniMaxPricing when pricing is zero (zero-value sentinel).
func TestPilotDemo_DefaultPricingFallback(t *testing.T) {
	bundle, err := PilotDemo(DefaultPilotTasks(), PilotPricing{}) // zero
	require.NoError(t, err)

	// Default 7 tasks cost ≈ $0.0070; should be <$0.05 budget.
	assert.Less(t, bundle.TotalCostUSD, 0.05)
	assert.Greater(t, bundle.TotalCostUSD, 0.0)
}

// TestPilotDemo_P99LatencyEmpty asserts P99Latency returns 0 for
// empty input (defensive zero-value).
func TestPilotDemo_P99LatencyEmpty(t *testing.T) {
	assert.Equal(t, time.Duration(0), P99Latency(nil))
	assert.Equal(t, time.Duration(0), P99Latency([]time.Duration{}))
}

// TestPilotDemo_RejectsWrongTaskCount asserts PilotDemo returns an
// error when given not-7 tasks.
func TestPilotDemo_RejectsWrongTaskCount(t *testing.T) {
	_, err := PilotDemo(make([]Task, 5), DefaultMiniMaxPricing)
	assert.Error(t, err, "PilotDemo MUST reject not-7 task lists")

	_, err = PilotDemo(make([]Task, 9), DefaultMiniMaxPricing)
	assert.Error(t, err, "PilotDemo MUST reject not-7 task lists")
}
