package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nfsarch33/helixon-eval/internal/agenteval"
	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/llmcost"
)

// TestDemoCmd_FiltersMiniMaxiModel_v18685_3 verifies that the demo
// subcommand restricts the 7-task canonical matrix to MiniMax-M3 only
// (i.e. 7 cases, not 21).
func TestDemoCmd_FiltersMiniMaxiModel_v18685_3(t *testing.T) {
	cmd := demoCmd()
	if cmd == nil {
		t.Fatal("demoCmd must not be nil")
	}
	if cmd.Use != "demo" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "demo")
	}
	// Run the underlying suite-build logic via the demo's runner helper.
	suite, err := buildDemoSuite("v18685-3-test")
	if err != nil {
		t.Fatalf("buildDemoSuite: %v", err)
	}
	if got, want := len(suite.Cases), len(agenteval.CanonicalTasks); got != want {
		t.Fatalf("demo cases = %d, want %d (7 tasks × MiniMax-M3 only)", got, want)
	}
	for _, c := range suite.Cases {
		if !strings.HasSuffix(c.Name, "__MiniMax-M3") {
			t.Errorf("case %q not scoped to MiniMax-M3", c.Name)
		}
	}
}

// TestDemoRun_WritesJSONAndNDJSON_v18685_3 verifies that running the
// demo suite writes (a) a JSON envelope to stdout and (b) one NDJSON
// line to the eval-results.ndjson path.
func TestDemoRun_WritesJSONAndNDJSON_v18685_3(t *testing.T) {
	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.ndjson")

	var stdout bytes.Buffer
	res, err := runDemo(context.Background(), "v18685-3-test", resultsPath, &stdout)
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}

	// (a) JSON envelope on stdout
	var got map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout.String())
	}
	if got["verdict"] != string(evalfw.VerdictPass) {
		t.Fatalf("verdict = %v, want PASS", got["verdict"])
	}
	if total, _ := got["total_cases"].(float64); int(total) != 7 {
		t.Fatalf("total_cases = %v, want 7", got["total_cases"])
	}

	// (b) NDJSON line on disk
	data, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("read results.ndjson: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("results.ndjson has %d lines, want 1", len(lines))
	}
	var ev evalfw.ReportEvent
	if err := json.Unmarshal(lines[0], &ev); err != nil {
		t.Fatalf("results.ndjson line not valid JSON: %v\n%s", err, string(lines[0]))
	}
	if ev.Total != 7 {
		t.Errorf("NDJSON Total = %d, want 7", ev.Total)
	}
	if ev.Verdict != evalfw.VerdictPass {
		t.Errorf("NDJSON Verdict = %s, want PASS", ev.Verdict)
	}
	_ = res
}

// TestDemoRun_CostUnderHalfDollar_v18685_3 verifies the demo's recorded
// cost is below $0.50 (the plan gate). The cost tracker is real (writes
// NDJSON to the configured path) so this also exercises llmcost.
func TestDemoRun_CostUnderHalfDollar_v18685_3(t *testing.T) {
	dir := t.TempDir()
	costPath := filepath.Join(dir, "cost.ndjson")
	resultsPath := filepath.Join(dir, "results.ndjson")

	tracker, err := llmcost.New(costPath)
	if err != nil {
		t.Fatalf("llmcost.New: %v", err)
	}

	totalUSD, err := runDemoWithCost(context.Background(), "v18685-3-test", resultsPath, tracker)
	if err != nil {
		t.Fatalf("runDemoWithCost: %v", err)
	}
	if totalUSD < 0 {
		t.Fatalf("totalUSD = %f, want >= 0", totalUSD)
	}
	if totalUSD > 0.50 {
		t.Fatalf("totalUSD = $%.4f, want <= $0.50 (plan gate)", totalUSD)
	}

	// Verify NDJSON line on disk for cost
	data, err := os.ReadFile(costPath)
	if err != nil {
		t.Fatalf("read cost.ndjson: %v", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		t.Fatal("cost.ndjson is empty — cost events were not flushed")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var sawMiniMax bool
	for {
		var ev llmcost.Event
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode cost line: %v", err)
		}
		if ev.Backend == llmcost.BackendMiniMaxi && ev.Model == "MiniMax-M3" {
			sawMiniMax = true
		}
	}
	if !sawMiniMax {
		t.Fatal("no MiniMax-M3 cost event found in NDJSON")
	}
}
