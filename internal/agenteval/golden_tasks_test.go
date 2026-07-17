package agenteval

import (
	"testing"
)

// TestCanonicalTasks_HasSevenTasks locks the canonical task set to exactly
// 7 entries. Adding/removing a task is a deliberate change (v18681-3 went
// from 5 to 7). Bumping this test is the way to assert the bump.
func TestCanonicalTasks_HasSevenTasks(t *testing.T) {
	if got, want := len(CanonicalTasks), 7; got != want {
		t.Errorf("len(CanonicalTasks) = %d, want %d (bump this test when adding tasks)", got, want)
	}
}

// TestCanonicalTasks_HasExpectedNames locks the exact task IDs so cross-sprint
// verdict reports stay stable. If a task is renamed, this test fails and the
// verdict-comparison tooling must be updated.
func TestCanonicalTasks_HasExpectedNames(t *testing.T) {
	want := []string{
		"tool_dispatch_correctness",
		"checkpoint_emits_metrics",
		"livechannel_open_close",
		"loopguard_trips_on_infinite",
		"rubric_compatibility_check",
		"idempotency_key_chain",    // v18681-3
		"json_diff_reconciliation", // v18681-3
	}
	if len(CanonicalTasks) != len(want) {
		t.Fatalf("len(CanonicalTasks) = %d, want %d", len(CanonicalTasks), len(want))
	}
	for i, w := range want {
		if i >= len(CanonicalTasks) {
			t.Errorf("missing task at index %d: %q", i, w)
			continue
		}
		if got := CanonicalTasks[i]; got != w {
			t.Errorf("CanonicalTasks[%d] = %q, want %q", i, got, w)
		}
	}
}

// TestCanonicalTasks_AllUnique ensures no duplicate task IDs.
func TestCanonicalTasks_AllUnique(t *testing.T) {
	seen := make(map[string]bool, len(CanonicalTasks))
	for _, t1 := range CanonicalTasks {
		if seen[t1] {
			t.Errorf("duplicate task id: %q", t1)
		}
		seen[t1] = true
	}
}
