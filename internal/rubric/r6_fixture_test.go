package rubric

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestR6_FixtureFile_CountAndFields checks the testdata/r6.jsonl fixture
// matches the live r6_full suite's case count and required field shape.
func TestR6_FixtureFile_CountAndFields(t *testing.T) {
	// Resolve fixture path relative to the rubric package directory.
	wd, _ := os.Getwd()
	path := filepath.Join(wd, "testdata", "r6.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var r struct {
			ID           string `json:"id"`
			Dim          string `json:"dim"`
			Prompt       string `json:"prompt"`
			Expect       string `json:"expect"`
			TokensInEst  int    `json:"tokens_in_est"`
			TokensOutEst int    `json:"tokens_out_est"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Errorf("invalid json line %d: %v", count, err)
		}
		if r.ID == "" || r.Dim == "" || r.Prompt == "" {
			t.Errorf("missing required fields at line %d", count)
		}
		count++
	}
	if count != 79 {
		t.Errorf("fixture has %d cases, want 79", count)
	}
}
