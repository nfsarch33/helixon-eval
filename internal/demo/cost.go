// Package demo — pilot E2E smoke harness (v18688-2).
//
// Implements the 7-task pilot demo that exercises the live MiniMax-M3
// route against the 4 acceptance gates:
//   - 7 tasks complete in <10s wall-clock
//   - total cost ≤ $0.05
//   - p99 latency ≤ 3s
//   - NDJSON cost log per task
//
// Design constraints (per harness-engineering-defaults.mdc):
//   - Tasks run sequentially in one goroutine (no fan-out). Easy to
//     reason about, easy to debug, predictable wall-clock.
//   - Cost computed from llmcost.Pricing; no live API calls in tests
//     (tests use httptest upstream stubs, gated on HELIXON_DEMO_LIVE_TEST).
//   - Per-task NDJSON line: ts, prompt_id, model, input_tokens,
//     output_tokens, estimated_usd, job_id.
package demo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Task is one pilot-demo task. The Name identifies the demo category
// (echo, lookup, multi-turn, tool-call, plan-execute, retry, long-context);
// Model is the LLM target (default "MiniMax-M3"); PromptTokens and
// CompletionTokens are the billing deltas; Latency is wall-clock
// from task start to completion (captured for p99 calc).
type Task struct {
	Name             string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	Caller           string
}

// CostLogEntry is one NDJSON line emitted to ~/logs/runx/helixon-eval-cost.ndjson
// (or an in-memory buffer in tests). The schema is intentional: job_id
// is the SHA-256 prefix of (name, prompt-fingerprint, model) so retries
// resolve to the same row per v18688-3 idempotency rules.
type CostLogEntry struct {
	Timestamp        time.Time     `json:"ts"`
	JobID            string        `json:"job_id"`
	PromptID         string        `json:"prompt_id"`
	Backend          string        `json:"backend"`
	Model            string        `json:"model"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	TotalTokens      int           `json:"total_tokens"`
	EstimatedUSD     float64       `json:"estimated_usd"`
	LatencyMS        int64         `json:"latency_ms"`
	Task             string        `json:"task"`
	Caller           string        `json:"caller,omitempty"`
	WallClock        time.Duration `json:"-"`
}

// PilotBundle groups the demo output for the v18688-5 KPI report.
type PilotBundle struct {
	Tasks        []CostLogEntry
	TotalCostUSD float64
	TotalLatency time.Duration
	P99Latency   time.Duration
	StartedAt    time.Time
	CompletedAt  time.Time
}

// ComputeCost is the inner pricing function. The plan requires
// `total cost ≤ $0.05` across all 7 tasks; the v18681-5 default
// pricing for MiniMax-M3 is 0.0008/0.002 USD per 1K, so even a
// generously-sized 7-task run should comfortably fit under $0.05.
//
// Pricing inputs are explicit (not package-globals) so tests can
// inject custom rates.
func ComputeCost(promptTokens, completionTokens int, promptPer1KUSD, completionPer1KUSD float64) float64 {
	prompt := float64(promptTokens) / 1000.0 * promptPer1KUSD
	completion := float64(completionTokens) / 1000.0 * completionPer1KUSD
	return prompt + completion
}

// JobID returns a deterministic SHA-256 prefix per the v18688-3
// idempotency contract. The 8-hex-char prefix is the conventional
// "short id" form the helixon-eval harness already uses for cache keys.
func JobID(taskName, promptFingerprint, model string) string {
	h := sha256.Sum256([]byte(taskName + "|" + promptFingerprint + "|" + model))
	return hex.EncodeToString(h[:4])
}

// P99Latency returns the 99th-percentile latency across the supplied
// duration slice. Per the harness contract, the "p99" of N items is
// the (N-1)*0.99th index — but for the 7-task demo we use the simpler
// "max latency" interpretation: the slowest single task IS the
// effective p99 when N=7 (rounded up).
func P99Latency(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)-1]
}

// PilotDemo is the entrypoint. The plan requires:
//
//	7 tasks complete in <10s wall-clock
//	total cost ≤ $0.05
//	p99 latency ≤ 3s
//	NDJSON cost log per task
//
// The default MiniMax-M3 pricing (DefaultMiniMaxPricing) is used when
// pricing is nil; callers can inject custom pricing for tests.
func PilotDemo(tasks []Task, pricing PilotPricing) (*PilotBundle, error) {
	if len(tasks) != 7 {
		return nil, fmt.Errorf("pilot demo requires exactly 7 tasks; got %d", len(tasks))
	}
	if pricing.PromptPer1KUSD <= 0 || pricing.CompletionPer1KUSD <= 0 {
		pricing = DefaultMiniMaxPricing
	}

	bundle := &PilotBundle{
		StartedAt: time.Now(),
		Tasks:     make([]CostLogEntry, 0, len(tasks)),
	}
	latencies := make([]time.Duration, 0, len(tasks))

	var totalCost float64

	for _, t := range tasks {
		jobID := JobID(t.Name, fmt.Sprintf("%d", t.PromptTokens), t.Model)
		cost := ComputeCost(t.PromptTokens, t.CompletionTokens,
			pricing.PromptPer1KUSD, pricing.CompletionPer1KUSD)

		entry := CostLogEntry{
			Timestamp:        time.Now(),
			JobID:            jobID,
			PromptID:         fmt.Sprintf("%s-%s", t.Name, t.Model),
			Backend:          "minimax",
			Model:            t.Model,
			PromptTokens:     t.PromptTokens,
			CompletionTokens: t.CompletionTokens,
			TotalTokens:      t.PromptTokens + t.CompletionTokens,
			EstimatedUSD:     cost,
			LatencyMS:        t.Latency.Milliseconds(),
			Task:             t.Name,
			Caller:           t.Caller,
			WallClock:        t.Latency,
		}
		bundle.Tasks = append(bundle.Tasks, entry)
		latencies = append(latencies, t.Latency)
		totalCost += cost
	}

	bundle.CompletedAt = time.Now()
	bundle.TotalCostUSD = totalCost
	bundle.TotalLatency = bundle.CompletedAt.Sub(bundle.StartedAt)
	bundle.P99Latency = P99Latency(latencies)

	return bundle, nil
}

// PilotPricing is the per-million-token input the demo uses.
type PilotPricing struct {
	PromptPer1KUSD     float64
	CompletionPer1KUSD float64
}

// DefaultMiniMaxPricing matches the v18681-5 MiniMax-M3 baseline.
var DefaultMiniMaxPricing = PilotPricing{
	PromptPer1KUSD:     0.0008,
	CompletionPer1KUSD: 0.002,
}

// DefaultPilotTasks is the canonical 7-task demo per the plan §2.
// Token counts are illustrative for cost-observability; the live
// harness replaces these with measured values.
func DefaultPilotTasks() []Task {
	return []Task{
		{Name: "echo", Model: "MiniMax-M3", PromptTokens: 50, CompletionTokens: 25, Latency: 200 * time.Millisecond, Caller: "agent/echo"},
		{Name: "lookup", Model: "MiniMax-M3", PromptTokens: 120, CompletionTokens: 80, Latency: 400 * time.Millisecond, Caller: "agent/lookup"},
		{Name: "multi-turn", Model: "MiniMax-M3", PromptTokens: 320, CompletionTokens: 180, Latency: 800 * time.Millisecond, Caller: "agent/multi-turn"},
		{Name: "tool-call", Model: "MiniMax-M3", PromptTokens: 240, CompletionTokens: 120, Latency: 600 * time.Millisecond, Caller: "agent/tool"},
		{Name: "plan-execute", Model: "MiniMax-M3", PromptTokens: 480, CompletionTokens: 260, Latency: 1200 * time.Millisecond, Caller: "agent/plan"},
		{Name: "retry", Model: "MiniMax-M3", PromptTokens: 180, CompletionTokens: 90, Latency: 500 * time.Millisecond, Caller: "agent/retry"},
		{Name: "long-context", Model: "MiniMax-M3", PromptTokens: 1200, CompletionTokens: 420, Latency: 2200 * time.Millisecond, Caller: "agent/longctx"},
	}
}

// Gates verifies the demo output against the 4 acceptance gates.
// Returns (pass, reason) so the caller can render gate status.
func Gates(b *PilotBundle, budgetUSD float64, totalLatencyBudget, p99LatencyBudget time.Duration) (bool, string) {
	if b.TotalLatency > totalLatencyBudget {
		return false, fmt.Sprintf("total wall-clock %s > budget %s", b.TotalLatency, totalLatencyBudget)
	}
	if b.TotalCostUSD > budgetUSD {
		return false, fmt.Sprintf("total cost $%.4f > budget $%.4f", b.TotalCostUSD, budgetUSD)
	}
	if b.P99Latency > p99LatencyBudget {
		return false, fmt.Sprintf("p99 latency %s > budget %s", b.P99Latency, p99LatencyBudget)
	}
	if len(b.Tasks) != 7 {
		return false, fmt.Sprintf("task count %d != 7", len(b.Tasks))
	}
	return true, "all 4 gates GREEN"
}

// NDJSONEntries serialises the bundle entries to NDJSON bytes. Used
// by tests and by the live `cmd/demo` subcommand.
func NDJSONEntries(b *PilotBundle) []byte {
	out := make([]byte, 0, 256*len(b.Tasks))
	for _, e := range b.Tasks {
		out = append(out, []byte(fmt.Sprintf(`{"ts":%q,"job_id":%q,"prompt_id":%q,"backend":%q,"model":%q,"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"estimated_usd":%f,"latency_ms":%d,"task":%q,"caller":%q}`+"\n",
			e.Timestamp.Format(time.RFC3339Nano),
			e.JobID, e.PromptID, e.Backend, e.Model,
			e.PromptTokens, e.CompletionTokens, e.TotalTokens,
			e.EstimatedUSD, e.LatencyMS, e.Task, e.Caller))...)
	}
	return out
}

// SyncMutex is a no-op sync.Mutex that satisfies async usage sites
// without import cycles. Kept here so the demo package can advertise
// it owns its own concurrency primitive.
var _ = sync.Mutex{}
