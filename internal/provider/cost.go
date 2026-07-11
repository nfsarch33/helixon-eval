package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PricingRate is the USD cost per 1000 tokens (in / out) for a model.
type PricingRate struct {
	InPerK  float64
	OutPerK float64
}

// pricingTable is the source of truth for R6 cost observability.
// Values match sop/llm-eval-r6-spec.md.
var pricingTable = map[string]PricingRate{
	"MiniMax-M3":   {InPerK: 0.0010, OutPerK: 0.0020},
	"qwen3.7-plus": {InPerK: 0.0004, OutPerK: 0.0012},
	"qwen3.7-max":  {InPerK: 0.0020, OutPerK: 0.0060},
}

// BudgetSentinel enforces a daily USD spend limit and emits an alert
// NDJSON when the accumulated cost crosses the threshold.
type BudgetSentinel struct {
	mu             sync.Mutex
	dailyLimitUSD  float64
	accumulatedUSD float64
	alertPath      string
}

// NewBudgetSentinel creates a sentinel with default $5.00/day limit
// and alert path at ~/logs/helixon-eval/cost-budget-alert.ndjson.
func NewBudgetSentinel() (*BudgetSentinel, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "logs", "helixon-eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &BudgetSentinel{
		dailyLimitUSD: 5.00,
		alertPath:     filepath.Join(dir, "cost-budget-alert.ndjson"),
	}, nil
}

// NewBudgetSentinelWithLimit creates a sentinel with a custom limit (test seam).
func NewBudgetSentinelWithLimit(limit float64, alertPath string) (*BudgetSentinel, error) {
	if err := os.MkdirAll(filepath.Dir(alertPath), 0o755); err != nil {
		return nil, err
	}
	return &BudgetSentinel{dailyLimitUSD: limit, alertPath: alertPath}, nil
}

// Record charges a single call's cost to the sentinel. If the running
// total crosses the daily limit, an alert NDJSON line is appended.
func (b *BudgetSentinel) Record(cost Cost, model string) (alerted bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.accumulatedUSD += cost.USD
	if b.accumulatedUSD > b.dailyLimitUSD {
		event := map[string]any{
			"ts":              time.Now().Format(time.RFC3339),
			"event":           "cost_budget_alert",
			"model":           model,
			"accumulated_usd": round2(b.accumulatedUSD),
			"limit_usd":       round2(b.dailyLimitUSD),
			"last_call_usd":   round2(cost.USD),
		}
		data, _ := json.Marshal(event)
		data = append(data, '\n')
		f, err := os.OpenFile(b.alertPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return false, err
		}
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Accumulated returns the current accumulated USD spend.
func (b *BudgetSentinel) Accumulated() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.accumulatedUSD
}

// Reset clears the daily accumulator (test seam).
func (b *BudgetSentinel) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.accumulatedUSD = 0
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100.0
}
