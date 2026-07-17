package llmcost

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecord_ComputesEstimatedUSD(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ev, err := tr.Record("job-1", BackendMiniMaxi, "MiniMax-M3", 1000, 500, "test")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	// 1000 prompt * 0.0008/1000 + 500 completion * 0.002/1000
	// = 0.0008 + 0.001 = 0.0018
	want := 0.0008 + 0.001
	if ev.EstimatedUSD < want-1e-9 || ev.EstimatedUSD > want+1e-9 {
		t.Errorf("EstimatedUSD = %f, want %f", ev.EstimatedUSD, want)
	}
	if ev.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", ev.TotalTokens)
	}
	if ev.JobID != "job-1" {
		t.Errorf("JobID = %q, want job-1", ev.JobID)
	}
}

func TestRecord_PersistsNDJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tr.Record("j-2", BackendQwenPlus, "qwen3.7-plus", 2000, 1000, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := tr.Record("j-3", BackendQwenMax, "qwen3.7-max", 3000, 2000, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d", len(lines))
	}
	for i, line := range lines {
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Errorf("line %d unmarshal: %v", i, err)
			continue
		}
		if ev.TotalTokens == 0 {
			t.Errorf("line %d: TotalTokens = 0", i)
		}
	}
}

func TestCheckDailyBudget_Triggers(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Push $0.05 of cost (0.0008 + 0.001 = 0.0018 per call; 28 calls = 0.05)
	for i := 0; i < 30; i++ {
		if _, err := tr.Record("budget-test", BackendMiniMaxi, "MiniMax-M3", 1000, 500, ""); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if !tr.CheckDailyBudget(DailyBudget{Backend: BackendMiniMaxi, MaxUSD: 0.05}) {
		t.Errorf("expected budget breach at $0.05")
	}
	if tr.CheckDailyBudget(DailyBudget{Backend: BackendMiniMaxi, MaxUSD: 10.0}) {
		t.Errorf("expected no breach at $10.0 cap")
	}
	if tr.CheckDailyBudget(DailyBudget{Backend: BackendMiniMaxi, MaxUSD: 0}) {
		t.Errorf("expected no breach when MaxUSD=0 (no cap)")
	}
}

func TestDailySummary_GroupsByBackend(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, b := range []Backend{BackendMiniMaxi, BackendQwenPlus, BackendQwenMax} {
		if _, err := tr.Record("summary", b, "model", 500, 250, ""); err != nil {
			t.Fatalf("Record %s: %v", b, err)
		}
	}
	day := time.Now().UTC().Format("2006-01-02")
	summary := tr.DailySummary(day)
	if len(summary) != 3 {
		t.Errorf("expected 3 backend summaries, got %d", len(summary))
	}
}

func TestPrettySummary_IncludesBackendAndTokens(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tr.Record("p-1", BackendMiniMaxi, "MiniMax-M3", 1000, 500, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var buf bytes.Buffer
	tr.PrettySummary(&buf)
	out := buf.String()
	if !strings.Contains(out, "minimax") {
		t.Errorf("summary missing backend: %q", out)
	}
	if !strings.Contains(out, "tokens") {
		t.Errorf("summary missing token count: %q", out)
	}
}

func TestSetPricing_Override(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "cost.ndjson")
	tr, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.SetPricing(BackendMiniMaxi, Pricing{PromptPer1KUSD: 0, CompletionPer1KUSD: 0})
	ev, err := tr.Record("override", BackendMiniMaxi, "MiniMax-M3", 1000, 1000, "")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if ev.EstimatedUSD != 0 {
		t.Errorf("override zero-pricing: EstimatedUSD = %f, want 0", ev.EstimatedUSD)
	}
}
