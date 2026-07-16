// v18667-1: coverage lift for internal/evalfw/report.go.
// Appended: tests for append-multiple, parent-dir creation edge case,
// aggregateMetrics edge cases (empty, averages, mixed). Existing
// TestDefaultReportPath_ContainsFileName + TestNewReportWriter_CreatesDir
// are preserved above.
package evalfw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	defer func() { _ = os.RemoveAll(dir) }()
	nested := filepath.Join(dir, "nested", "deep", "report.ndjson")
	w, err := NewReportWriter(nested)
	if err != nil {
		t.Fatalf("NewReportWriter: %v", err)
	}
	if w.path != nested {
		t.Errorf("path = %q, want %q", w.path, nested)
	}
}

// TestReportWriter_AppendsValidNDJSON verifies Write produces one
// valid NDJSON line per call (the first line for a fresh file).
func TestReportWriter_AppendsValidNDJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "report.ndjson")
	w, err := NewReportWriter(tmp)
	if err != nil {
		t.Fatalf("NewReportWriter: %v", err)
	}
	result := &SuiteResult{
		Name:       "smoke",
		Verdict:    VerdictPass,
		TotalCases: 1,
		Passed:     1,
	}
	if err := w.Write(result); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 NDJSON line, got %d: %q", len(lines), string(data))
	}
	var ev ReportEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ev.Suite != "smoke" || ev.Verdict != VerdictPass {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Timestamp == "" {
		t.Fatalf("Timestamp must be set")
	}
}

// TestReportWriter_AppendsMultipleEvents verifies subsequent Writes
// append rather than overwrite.
func TestReportWriter_AppendsMultipleEvents(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "report.ndjson")
	w, _ := NewReportWriter(tmp)
	for i, verdict := range []Verdict{VerdictPass, VerdictWarn, VerdictFail} {
		if err := w.Write(&SuiteResult{
			Name: "smoke", Verdict: verdict,
			TotalCases: 1, Passed: 1, Failed: i,
		}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(tmp)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 NDJSON lines, got %d", len(lines))
	}
}

// TestAggregateMetrics_EmptyCases verifies the nil-result branch.
func TestAggregateMetrics_EmptyCases(t *testing.T) {
	result := &SuiteResult{Name: "empty", Verdict: VerdictPass}
	got := aggregateMetrics(result)
	if got != nil {
		t.Errorf("aggregateMetrics(empty) = %v, want nil", got)
	}
}

// TestAggregateMetrics_AveragesPerMetric verifies the average
// computation across multiple cases.
func TestAggregateMetrics_AveragesPerMetric(t *testing.T) {
	result := &SuiteResult{
		Name:    "multi",
		Verdict: VerdictPass,
		Cases: []CaseResult{
			{Name: "c1", Verdict: VerdictPass, Metrics: map[string]float64{"latency_ms": 100}},
			{Name: "c2", Verdict: VerdictPass, Metrics: map[string]float64{"latency_ms": 200}},
			{Name: "c3", Verdict: VerdictPass, Metrics: map[string]float64{"errors": 1}},
		},
	}
	got := aggregateMetrics(result)
	if got["latency_ms"] != 300 {
		t.Errorf("sum latency_ms = %v, want 300", got["latency_ms"])
	}
	if got["latency_ms_avg"] != 150 {
		t.Errorf("avg latency_ms = %v, want 150", got["latency_ms_avg"])
	}
	if got["errors"] != 1 {
		t.Errorf("sum errors = %v, want 1", got["errors"])
	}
	if got["errors_avg"] != 1 {
		t.Errorf("avg errors = %v, want 1 (single case, avg = sum)", got["errors_avg"])
	}
}

// TestAggregateMetrics_EmptyMetricsOnEachCase verifies that cases
// with empty metrics don't add noise to the aggregate.
func TestAggregateMetrics_EmptyMetricsOnEachCase(t *testing.T) {
	result := &SuiteResult{
		Name: "no-metrics",
		Cases: []CaseResult{
			{Name: "c1", Verdict: VerdictPass},
			{Name: "c2", Verdict: VerdictPass},
		},
	}
	got := aggregateMetrics(result)
	if len(got) != 0 {
		t.Errorf("aggregateMetrics(no-metrics) = %v, want empty", got)
	}
}
