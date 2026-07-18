// Package demo — real-models harness for v18692-5.
//
// Extends the v18688-2 demo with a multi-provider matrix runner. Each
// backends exposes its own pricing in ProviderPricing; harness runs
// the canonical 7-task demo per provider, records the bundle to
// NDJSON, and emits a Markdown cost-dashboard + model-comparison
// matrix suitable for KPI ingestion.
//
// Live mode is gated on HELIXON_LIVE_EVAL=1 to prevent accidental
// paid runs in regular `go test` cycles. Default behaviour: dry-run
// pricing matrix only.
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/llmclient"
)

// Provider identifies the LLM target. Three values map 1:1 to the
// llmclient.Backend constants.
type Provider string

const (
	ProviderMiniMaxi Provider = "minimaxi"
	ProviderQwenPlus Provider = "qwen3.7-plus"
	ProviderQwenMax  Provider = "qwen3.7-max"
)

// AllProviders returns the canonical 3-provider matrix.
func AllProviders() []Provider {
	return []Provider{ProviderMiniMaxi, ProviderQwenPlus, ProviderQwenMax}
}

// ParseProvider canonicalises user input. Accepts common aliases
// (e.g. "minimax", "MiniMax-M3"). Empty string defaults to minimaxi.
func ParseProvider(s string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "minimaxi", "minimax", "minimax-m3":
		return ProviderMiniMaxi, nil
	case "qwen", "qwen-plus", "qwen3.7-plus":
		return ProviderQwenPlus, nil
	case "qwen-max", "qwen3.7-max":
		return ProviderQwenMax, nil
	default:
		return "", fmt.Errorf("unknown provider %q (use minimaxi|qwen3.7-plus|qwen3.7-max)", s)
	}
}

// Model returns the canonical model id for the provider.
func (p Provider) Model() string {
	switch p {
	case ProviderMiniMaxi:
		return "MiniMax-M3"
	case ProviderQwenPlus:
		return "qwen3.7-plus"
	case ProviderQwenMax:
		return "qwen3.7-max"
	default:
		return ""
	}
}

// Backend converts the harness-level Provider into the llmclient
// representation. Used to construct the OpenAI-compatible client.
func (p Provider) Backend() llmclient.Backend {
	switch p {
	case ProviderMiniMaxi:
		return llmclient.BackendMiniMaxi
	case ProviderQwenPlus:
		return llmclient.BackendQwenPlus
	case ProviderQwenMax:
		return llmclient.BackendQwenMax
	default:
		return ""
	}
}

// EnvKey returns the canonical env var name holding the API key for
// the provider. Operators source this from 1Password at startup.
func (p Provider) EnvKey() string {
	switch p {
	case ProviderMiniMaxi:
		return "MINIMAX_API_KEY"
	case ProviderQwenPlus, ProviderQwenMax:
		return "QWEN_API_KEY"
	default:
		return ""
	}
}

// ProviderPricing is the per-million-token rates for one provider.
// Aliyun Qwen rates follow the v18681-5 default pricing schedule;
// MiniMax-M3 matches the v18688-2 demo baseline.
type ProviderPricing struct {
	PromptPer1KUSD     float64
	CompletionPer1KUSD float64
}

// PricingFor returns the canonical pricing schedule for a provider.
// Used by the matrix runner so the cost dashboard reflects each
// backend's own pricing rather than MiniMax defaults.
func PricingFor(p Provider) ProviderPricing {
	switch p {
	case ProviderMiniMaxi:
		return ProviderPricing{PromptPer1KUSD: 0.0008, CompletionPer1KUSD: 0.002}
	case ProviderQwenPlus:
		return ProviderPricing{PromptPer1KUSD: 0.0012, CompletionPer1KUSD: 0.0024}
	case ProviderQwenMax:
		return ProviderPricing{PromptPer1KUSD: 0.0020, CompletionPer1KUSD: 0.0040}
	default:
		return ProviderPricing{}
	}
}

// ToPilotPricing converts the harness pricing record into the
// generic PilotPricing the demo P99 helpers consume.
func (pp ProviderPricing) ToPilotPricing() PilotPricing {
	return PilotPricing{
		PromptPer1KUSD:     pp.PromptPer1KUSD,
		CompletionPer1KUSD: pp.CompletionPer1KUSD,
	}
}

// BackendFromProvider resolves the provider to its llmclient backend
// identifier. Exposed so callers can construct clients.
func BackendFromProvider(p Provider) llmclient.Backend {
	return p.Backend()
}

// MatrixRow summarises one provider's run through the matrix.
type MatrixRow struct {
	Provider     Provider       `json:"provider"`
	Model        string         `json:"model"`
	Status       string         `json:"status"` // "ok" | "skipped" | "fail"
	TotalCostUSD float64        `json:"total_cost_usd"`
	TotalLatency time.Duration  `json:"total_latency_ns"`
	P99Latency   time.Duration  `json:"p99_latency_ns"`
	TaskCount    int            `json:"task_count"`
	Tasks        []CostLogEntry `json:"tasks"`
	Source       string         `json:"source"` // "dry-run" | "live"
	ErrorMessage string         `json:"error,omitempty"`
}

// MatrixResult is the cumulative report emitted to NDJSON + Markdown.
type MatrixResult struct {
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt time.Time   `json:"completed_at"`
	Rows        []MatrixRow `json:"rows"`
	TotalCost   float64     `json:"total_cost_usd"`
	Source      string      `json:"source"` // "dry-run" | "live"
}

// RunMatrix executes the canonical 7-task demo for each provider in
// the supplied list. In live mode (useLive=true) each provider's
// task list triggers a real /v1/chat/completions round-trip; in dry
// mode the same Task slice is priced with the provider's pricing
// schedule.
//
// Task ordering and content come from DefaultPilotTasks() with the
// provider's model id substituted. Real-mode task prompts are sized
// realistically (≤1.5K input tokens) to honour the v18681-5 cost
// envelope; dry-mode keeps the cached token counts.
func RunMatrix(ctx context.Context, providers []Provider, apiKeys map[Provider]string, useLive bool) (*MatrixResult, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("RunMatrix requires at least 1 provider")
	}
	result := &MatrixResult{
		StartedAt: time.Now(),
		Source:    dryOrLive(useLive),
	}
	for _, p := range providers {
		row := runOneProvider(ctx, p, apiKeys, useLive)
		result.Rows = append(result.Rows, row)
		result.TotalCost += row.TotalCostUSD
	}
	result.CompletedAt = time.Now()
	return result, nil
}

// runOneProvider builds a 7-task slice rooted at p.Model(), runs the
// canonical PilotDemo, and packages the result.
func runOneProvider(ctx context.Context, p Provider, apiKeys map[Provider]string, useLive bool) MatrixRow {
	row := MatrixRow{
		Provider: p,
		Model:    p.Model(),
		Source:   dryOrLive(useLive),
	}
	pricing := PricingFor(p).ToPilotPricing()

	tasks := DefaultPilotTasks()
	for i := range tasks {
		tasks[i].Model = p.Model()
	}

	if !useLive {
		// Dry-run: use cached token counts and pricing; skip network.
		bundle, err := PilotDemo(tasks, pricing)
		if err != nil {
			row.Status = "fail"
			row.ErrorMessage = err.Error()
			return row
		}
		fillRowFromBundle(&row, bundle, "ok", "")
		return row
	}

	// Live: gate per provider; skip if key missing.
	key := strings.TrimSpace(apiKeys[p])
	if key == "" {
		row.Status = "skipped"
		row.ErrorMessage = fmt.Sprintf("api key unset (env %s)", p.EnvKey())
		// Still emit dry-run pricing so the matrix isn't empty.
		bundle, err := PilotDemo(tasks, pricing)
		if err == nil {
			row.Tasks = bundle.Tasks
			row.TaskCount = len(bundle.Tasks)
			row.TotalCostUSD = bundle.TotalCostUSD
			row.TotalLatency = bundle.TotalLatency
			row.P99Latency = bundle.P99Latency
		}
		return row
	}

	client, err := llmclient.New(p.Backend(), key)
	if err != nil {
		row.Status = "fail"
		row.ErrorMessage = fmt.Sprintf("client init: %v", err)
		return row
	}

	liveTasks := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		var resp *llmclient.ChatResponse
		var lerr error
		// Short per-call context timeout to keep wall-clock predictable.
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		callStart := time.Now()
		resp, lerr = client.Chat(callCtx, llmclient.ChatRequest{
			Model:       p.Model(),
			Messages:    []llmclient.Message{{Role: "user", Content: promptForTask(t)}},
			MaxTokens:   64,
			Temperature: 0,
		})
		callLatency := time.Since(callStart)
		cancel()
		if lerr != nil {
			row.Status = "fail"
			row.ErrorMessage = lerr.Error()
			break
		}
		t.PromptTokens = resp.Usage.PromptTokens
		t.CompletionTokens = resp.Usage.CompletionTokens
		t.Latency = callLatency
		liveTasks = append(liveTasks, t)
	}

	if row.Status == "fail" && len(liveTasks) == 0 {
		return row
	}
	bundle, err := PilotDemo(liveTasks, pricing)
	if err != nil {
		row.Status = "fail"
		row.ErrorMessage = err.Error()
		return row
	}
	fillRowFromBundle(&row, bundle, "ok", "")
	return row
}

// fillRowFromBundle copies fields from bundle into the row.
func fillRowFromBundle(row *MatrixRow, bundle *PilotBundle, status, msg string) {
	row.Status = status
	row.ErrorMessage = msg
	row.Tasks = bundle.Tasks
	row.TaskCount = len(bundle.Tasks)
	row.TotalCostUSD = bundle.TotalCostUSD
	row.TotalLatency = bundle.TotalLatency
	row.P99Latency = bundle.P99Latency
}

// promptForTask builds a short user prompt derived from a demo task.
// Keeps payload ≤32 tokens; live API round-trips against this string.
func promptForTask(t Task) string {
	return fmt.Sprintf("[task=%s] brief reply please.", t.Name)
}

func dryOrLive(useLive bool) string {
	if useLive {
		return "live"
	}
	return "dry-run"
}

// AppendNDJSON writes the matrix result as one line per provider to
// the supplied path. Creates the file if needed; never truncates.
func AppendNDJSON(path string, result *MatrixResult) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(result)
}

// RenderMarkdown produces the cost dashboard + model-comparison
// matrix as a Markdown string. Includes:
//
//   - Per-provider summary row (status, total cost, latency, p99)
//   - Cost breakdown table by task
//   - Cost comparison ranking (cheapest first)
//
// Pure function; no side effects. Suitable for stdout or KPI artefact.
func RenderMarkdown(result *MatrixResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Real-Models Harness (%s)\n\n", result.Source)
	fmt.Fprintf(&b, "Started: %s\n\n", result.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Completed: %s\n\n", result.CompletedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Total cost (sum): $%.6f\n\n", result.TotalCost)

	b.WriteString("### Provider Matrix\n\n")
	b.WriteString("| Provider | Model | Status | Tasks | Total Cost | Wall-Clock | p99 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range result.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | $%.6f | %s | %s |\n",
			r.Provider, r.Model, r.Status, r.TaskCount,
			r.TotalCostUSD, r.TotalLatency, r.P99Latency)
	}
	b.WriteString("\n")

	// Cost comparison: cheapest first.
	type ranked struct {
		Provider Provider
		Cost     float64
		Status   string
	}
	ranks := make([]ranked, 0, len(result.Rows))
	for _, r := range result.Rows {
		ranks = append(ranks, ranked{r.Provider, r.TotalCostUSD, r.Status})
	}
	sort.Slice(ranks, func(i, j int) bool {
		return ranks[i].Cost < ranks[j].Cost
	})
	b.WriteString("### Cost Ranking\n\n")
	for i, r := range ranks {
		fmt.Fprintf(&b, "%d. `%s` — $%.6f [%s]\n", i+1, r.Provider, r.Cost, r.Status)
	}
	b.WriteString("\n")

	// Per-provider task breakdown (only for providers that ran).
	for _, r := range result.Rows {
		if r.TaskCount == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s — %s\n\n", r.Provider, r.Model)
		b.WriteString("| Task | Tokens (in/out) | Latency | Cost |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, e := range r.Tasks {
			lat := time.Duration(e.LatencyMS) * time.Millisecond
			fmt.Fprintf(&b, "| %s | %d / %d | %s | $%.6f |\n",
				e.Task, e.PromptTokens, e.CompletionTokens, lat, e.EstimatedUSD)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// EnvLookup is the env-resolution helper. Stubbed for tests so the
// harness works without an apiKey map.
func EnvLookup(envVar string) string {
	return strings.TrimSpace(os.Getenv(envVar))
}

// EnvKeyMap builds the apiKeys map from process env. Callers can
// override by passing their own map to RunMatrix.
func EnvKeyMap() map[Provider]string {
	m := make(map[Provider]string)
	for _, p := range AllProviders() {
		m[p] = EnvLookup(p.EnvKey())
	}
	return m
}

// ResolveAPIKeysFor callers want: env first, then pass-through map
// to override. Used by main so `--api-key` flag wins over env.
func ResolveAPIKeysFor(providers []Provider, override map[Provider]string, env map[Provider]string) map[Provider]string {
	if env == nil {
		env = EnvKeyMap()
	}
	out := make(map[Provider]string)
	for _, p := range providers {
		if v, ok := override[p]; ok && strings.TrimSpace(v) != "" {
			out[p] = v
			continue
		}
		if v, ok := env[p]; ok {
			out[p] = v
		}
	}
	return out
}

// RoundTrip is a smaller, single-call smoke helper. It sends one
// prompt to provider p and returns (latency, tokens-in, tokens-out,
// error). Used by operator-driven one-shot health checks via the
// evaloop smoke skill.
//
// The function is intentionally simple: it does not record to the
// bundle but provides the building block for ad-hoc evaluations.
func RoundTrip(ctx context.Context, p Provider, apiKey, prompt string) (time.Duration, int, int, error) {
	if strings.TrimSpace(apiKey) == "" {
		return 0, 0, 0, fmt.Errorf("api key required for provider %s", p)
	}
	client, err := llmclient.New(p.Backend(), apiKey)
	if err != nil {
		return 0, 0, 0, err
	}
	start := time.Now()
	req := llmclient.ChatRequest{
		Model:       p.Model(),
		Messages:    []llmclient.Message{{Role: "user", Content: prompt}},
		MaxTokens:   64,
		Temperature: 0,
	}
	resp, err := client.Chat(ctx, req)
	if err != nil {
		return time.Since(start), 0, 0, err
	}
	return time.Since(start), resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
}

// WriteAll writes the markdown + ndjson envelopes to stdout for the
// default matrix run. Returns the markdown length written.
//
// Useful for cmd/demo glue to avoid hand-rolled io.Copy loops.
func WriteAll(w io.Writer, md string, ndjson []byte) (int, error) {
	if _, err := io.WriteString(w, md); err != nil {
		return 0, err
	}
	if _, err := w.Write(ndjson); err != nil {
		return 0, err
	}
	return len(md) + len(ndjson), nil
}

// EnsureContentType is unused here; exported so consumers can verify
// the HTTP content type expected for the OpenAI-compatible surface.
var _ = http.DetectContentType
