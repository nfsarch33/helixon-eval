// Package llmcost tracks per-call cost for the three production LLM
// backends, emits NDJSON cost events to ~/logs/runx/helixon-eval-cost.ndjson,
// and reports daily-budget alerts. Per v18681-5 (cost observability
// axis). All costs are estimates from the per-million-token rates
// published by the providers.
package llmcost

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Backend identifies a pricing source.
type Backend string

const (
	BackendMiniMaxi Backend = "minimax"
	BackendQwenPlus Backend = "qwen-plus"
	BackendQwenMax  Backend = "qwen-max"
)

// Pricing per 1K tokens. Numbers are illustrative; operator should
// override with the latest published rates.
type Pricing struct {
	PromptPer1KUSD     float64
	CompletionPer1KUSD float64
}

// DefaultPricing returns the v18681-5 baseline pricing.
func DefaultPricing() map[Backend]Pricing {
	return map[Backend]Pricing{
		BackendMiniMaxi: {PromptPer1KUSD: 0.0008, CompletionPer1KUSD: 0.002},
		BackendQwenPlus: {PromptPer1KUSD: 0.0004, CompletionPer1KUSD: 0.0012},
		BackendQwenMax:  {PromptPer1KUSD: 0.0012, CompletionPer1KUSD: 0.004},
	}
}

// Event is a single cost observation, emitted as one JSON line.
type Event struct {
	Timestamp        time.Time `json:"ts"`
	JobID            string    `json:"job_id"`
	Backend          Backend   `json:"backend"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	EstimatedUSD     float64   `json:"estimated_usd"`
	Caller           string    `json:"caller,omitempty"`
}

// Tracker accumulates cost events and flushes them to a NDJSON file.
type Tracker struct {
	mu      sync.Mutex
	path    string
	pricing map[Backend]Pricing
	// totals is keyed by "YYYY-MM-DD|backend" to support per-day per-backend
	// aggregation without nested maps.
	totals map[string]*DailyTotal
}

// DailyTotal is the running total for a given day + backend.
type DailyTotal struct {
	Day     string // YYYY-MM-DD (UTC)
	Backend Backend
	USD     float64
	Tokens  int
}

// New returns a Tracker that writes to path. If path is empty, the default
// ~/logs/runx/helixon-eval-cost.ndjson is used.
func New(path string) (*Tracker, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home: %w", err)
		}
		path = filepath.Join(home, "logs", "runx", "helixon-eval-cost.ndjson")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	return &Tracker{
		path:    path,
		pricing: DefaultPricing(),
		totals:  make(map[string]*DailyTotal),
	}, nil
}

// SetPricing overrides the default pricing for a backend.
func (t *Tracker) SetPricing(b Backend, p Pricing) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pricing == nil {
		t.pricing = make(map[Backend]Pricing)
	}
	t.pricing[b] = p
}

// Record appends an event, flushes NDJSON, and updates the daily total.
// Returns the event recorded.
func (t *Tracker) Record(jobID string, backend Backend, model string, promptTok, completionTok int, caller string) (*Event, error) {
	ev := &Event{
		Timestamp:        time.Now().UTC(),
		JobID:            jobID,
		Backend:          backend,
		Model:            model,
		PromptTokens:     promptTok,
		CompletionTokens: completionTok,
		TotalTokens:      promptTok + completionTok,
		Caller:           caller,
	}
	t.mu.Lock()
	pr := t.pricing[backend]
	t.mu.Unlock()
	ev.EstimatedUSD = (float64(promptTok)*pr.PromptPer1KUSD + float64(completionTok)*pr.CompletionPer1KUSD) / 1000.0

	if err := t.append(ev); err != nil {
		return nil, fmt.Errorf("append ndjson: %w", err)
	}
	t.updateDaily(ev)
	return ev, nil
}

func (t *Tracker) append(ev *Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(ev)
}

func (t *Tracker) updateDaily(ev *Event) {
	day := ev.Timestamp.Format("2006-01-02")
	key := day + "|" + string(ev.Backend)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.totals[key] == nil {
		t.totals[key] = &DailyTotal{Day: day, Backend: ev.Backend}
	}
	t.totals[key].USD += ev.EstimatedUSD
	t.totals[key].Tokens += ev.TotalTokens
}

// DailyBudget is a per-day USD cap. Zero or negative means no cap.
type DailyBudget struct {
	Backend Backend
	MaxUSD  float64
}

// CheckDailyBudget returns true if the running total for today's
// backend is at or over the budget. Caller passes the budget.
func (t *Tracker) CheckDailyBudget(b DailyBudget) bool {
	if b.MaxUSD <= 0 {
		return false
	}
	day := time.Now().UTC().Format("2006-01-02")
	key := day + "|" + string(b.Backend)
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.totals[key]
	if !ok {
		return false
	}
	return d.USD >= b.MaxUSD
}

// DailySummary returns a slice of DailyTotal for the given day, or
// today if day == "". Result is sorted by Backend for stable output.
func (t *Tracker) DailySummary(day string) []DailyTotal {
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []DailyTotal
	for _, d := range t.totals {
		if d.Day == day {
			out = append(out, *d)
		}
	}
	return out
}

// PrettySummary writes a human-readable summary to w.
func (t *Tracker) PrettySummary(w io.Writer) {
	day := time.Now().UTC().Format("2006-01-02")
	fmt.Fprintf(w, "Cost summary for %s (UTC):\n", day)
	for _, d := range t.DailySummary(day) {
		fmt.Fprintf(w, "  %-12s  $%.6f  %d tokens\n", d.Backend, d.USD, d.Tokens)
	}
}
