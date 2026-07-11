package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBudgetSentinel_RecordBelowLimit_NoAlert(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "alerts.ndjson")
	b, err := NewBudgetSentinelWithLimit(1.00, tmp)
	if err != nil {
		t.Fatal(err)
	}
	alerted, err := b.Record(Cost{USD: 0.10}, "MiniMax-M3")
	if err != nil {
		t.Fatal(err)
	}
	if alerted {
		t.Errorf("alerted=true want false")
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("alert file created but no alert expected")
	}
}

func TestBudgetSentinel_RecordCrossLimit_Alert(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "alerts.ndjson")
	b, err := NewBudgetSentinelWithLimit(0.50, tmp)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = b.Record(Cost{USD: 0.30}, "MiniMax-M3")
	alerted, err := b.Record(Cost{USD: 0.30}, "MiniMax-M3")
	if err != nil {
		t.Fatal(err)
	}
	if !alerted {
		t.Fatalf("alerted=false want true")
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &event); err != nil {
		t.Fatalf("invalid alert json: %v", err)
	}
	if event["event"] != "cost_budget_alert" {
		t.Errorf("event=%v", event["event"])
	}
}

func TestPricingTable_ContainsAllR6Models(t *testing.T) {
	for _, m := range []string{"MiniMax-M3", "qwen3.7-plus", "qwen3.7-max"} {
		if _, ok := pricingTable[m]; !ok {
			t.Errorf("missing pricing for %s", m)
		}
	}
}
