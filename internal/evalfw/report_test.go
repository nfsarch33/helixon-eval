package evalfw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultReportPath_ContainsFileName(t *testing.T) {
	p := DefaultReportPath()
	if !strings.Contains(p, "eval-results.ndjson") {
		t.Errorf("expected default path to contain eval-results.ndjson, got %s", p)
	}
}

func TestNewReportWriter_CreatesDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "eval-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	nested := filepath.Join(dir, "nested", "deep", "report.ndjson")
	w, err := NewReportWriter(nested)
	if err != nil {
		t.Fatalf("NewReportWriter: %v", err)
	}
	if w.path != nested {
		t.Errorf("path mismatch: %s vs %s", w.path, nested)
	}
}

func TestReportWriter_WritesNDJSON(t *testing.T) {
	tmp, err := os.CreateTemp("", "eval-test-*.ndjson")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name())

	w, _ := NewReportWriter(tmp.Name())
	sr := &SuiteResult{Name: "test", Verdict: VerdictPass, TotalCases: 2, Passed: 2, Duration: 100 * time.Millisecond}
	if err := w.Write(sr); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, _ := os.ReadFile(tmp.Name())
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 NDJSON line, got %d", len(lines))
	}
	var event ReportEvent
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("invalid NDJSON: %v", err)
	}
	if event.Suite != "test" {
		t.Errorf("suite=%q, want test", event.Suite)
	}
	if event.Passed != 2 {
		t.Errorf("passed=%d, want 2", event.Passed)
	}
}

func TestReportWriter_Appends(t *testing.T) {
	tmp, _ := os.CreateTemp("", "eval-test-*.ndjson")
	defer os.Remove(tmp.Name())
	w, _ := NewReportWriter(tmp.Name())
	for i := 0; i < 3; i++ {
		if err := w.Write(&SuiteResult{Name: "x", Verdict: VerdictPass, TotalCases: 1, Passed: 1}); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}
	data, _ := os.ReadFile(tmp.Name())
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestReportWriter_AggregateMetrics(t *testing.T) {
	tmp, _ := os.CreateTemp("", "eval-test-*.ndjson")
	defer os.Remove(tmp.Name())
	w, _ := NewReportWriter(tmp.Name())
	sr := &SuiteResult{
		Name: "metrics", Verdict: VerdictPass, TotalCases: 2, Passed: 2,
		Cases: []CaseResult{
			{Name: "a", Verdict: VerdictPass, Metrics: map[string]float64{"x": 10}},
			{Name: "b", Verdict: VerdictPass, Metrics: map[string]float64{"x": 20}},
		},
	}
	if err := w.Write(sr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(tmp.Name())
	var event ReportEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("invalid NDJSON: %v", err)
	}
	if event.Metrics["x"] != 30 {
		t.Errorf("x=%v, want 30", event.Metrics["x"])
	}
	if event.Metrics["x_avg"] != 15 {
		t.Errorf("x_avg=%v, want 15", event.Metrics["x_avg"])
	}
}

func TestReportWriter_NoMetricsReturnsNil(t *testing.T) {
	tmp, _ := os.CreateTemp("", "eval-test-*.ndjson")
	defer os.Remove(tmp.Name())
	w, _ := NewReportWriter(tmp.Name())
	if err := w.Write(&SuiteResult{Name: "empty", Verdict: VerdictPass}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(tmp.Name())
	if !strings.Contains(string(data), `"metrics":{}`) && !strings.Contains(string(data), `"metrics":null`) {
		t.Errorf("expected empty/null metrics, got %s", string(data))
	}
}
