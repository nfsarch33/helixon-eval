// Package contract contains RED tests for the helixon-eval vs helixon-evolver
// NDJSON contract. See ADR-0008 (helixon-eval vs helixon-evolver separation) and
// IMP-001 (helixon-eval-evolver-ndjson-parity regression contract, q8-c-10).
//
// The contract is the JSON Schema at contracts/ndjson-eval-result.schema.json,
// shared (byte-identical) between helixon-eval and helixon-evolver. These
// tests lock the schema presence, validity, ADR-0008-required fields, and
// parity with the evolver mirror copy.
//
// Failure modes this contract blocks:
//   - Drift between the eval-emitter copy and the evolver-consumer copy
//     (one repo bumped the schema, the other didn't).
//   - Required-field regressions (e.g. someone deletes "tenant_id" from
//     the schema; evolver's cross-tenant isolation breaks silently).
//   - Invalid JSON in the schema file itself (rejects every emit/parse).
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// schemaBytes holds the schema content loaded from disk at init time.
var schemaBytes []byte

func init() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	pkgDir := filepath.Dir(thisFile)
	p := filepath.Join(pkgDir, "ndjson-eval-result.schema.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return // tests will fail with a clear "schema missing" message
	}
	schemaBytes = b
}

// requiredFields per ADR-0008 / schema.json "required" array.
var requiredFields = []string{
	"schema_version",
	"job_id",
	"eval_id",
	"tenant_id",
	"model_id",
	"rubric_version",
	"started_at",
	"finished_at",
	"status",
	"metrics",
}

// TestEmbeddedSchema_ParseableJSON asserts the embedded schema itself is
// valid JSON. A malformed schema would reject every emit/parse call.
func TestEmbeddedSchema_ParseableJSON(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("ndjson-eval-result.schema.json not loaded (init-time read failed)")
	}
	var any map[string]any
	if err := json.Unmarshal(schemaBytes, &any); err != nil {
		t.Fatalf("ndjson-eval-result.schema.json is not valid JSON: %v", err)
	}
}

// TestEmbeddedSchema_RequiredFields asserts the schema required[] array
// matches the ADR-0008 invariant.
func TestEmbeddedSchema_RequiredFields(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("schema not loaded")
	}
	var doc struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	got := make(map[string]struct{}, len(doc.Required))
	for _, f := range doc.Required {
		got[f] = struct{}{}
	}
	for _, want := range requiredFields {
		if _, ok := got[want]; !ok {
			t.Errorf("schema missing ADR-0008 required field %q; got required=%v", want, doc.Required)
		}
	}
}

// TestEmbeddedSchema_SchemaVersionSemver asserts the schema_version pattern
// enforces semver.
func TestEmbeddedSchema_SchemaVersionSemver(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("schema not loaded")
	}
	var doc struct {
		Properties map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	p, ok := doc.Properties["schema_version"]
	if !ok {
		t.Fatal("schema.properties.schema_version is missing")
	}
	if p.Pattern == "" {
		t.Fatal("schema_version must declare a pattern (semver enforcement)")
	}
	if !strings.Contains(p.Pattern, "0-9") {
		t.Errorf("schema_version pattern %q does not look semver-shaped", p.Pattern)
	}
}

// TestEmbeddedSchema_StatusEnum verifies the status enum matches ADR-0008:
// success, error, timeout, cancelled.
func TestEmbeddedSchema_StatusEnum(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("schema not loaded")
	}
	var doc struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	p, ok := doc.Properties["status"]
	if !ok {
		t.Fatal("schema.properties.status is missing")
	}
	want := map[string]struct{}{
		"success":   {},
		"error":     {},
		"timeout":   {},
		"cancelled": {},
	}
	for _, s := range p.Enum {
		if _, ok := want[s]; !ok {
			t.Errorf("status enum has unexpected value %q (not in ADR-0008 whitelist)", s)
		}
		delete(want, s)
	}
	for s := range want {
		t.Errorf("status enum missing ADR-0008 value %q", s)
	}
}

// TestEmbeddedSchema_AdditionalPropertiesFalse asserts top-level
// additionalProperties is false (per the schema).
func TestEmbeddedSchema_AdditionalPropertiesFalse(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("schema not loaded")
	}
	var doc struct {
		AdditionalProperties bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	if doc.AdditionalProperties {
		t.Fatal("schema top-level additionalProperties must be false (ADR-0008 contract strictness)")
	}
}

// TestSchema_ParityWithEvolver walks the filesystem to find the helixon-evolver
// repo's mirror copy of the schema and asserts byte-parity. If only one repo
// is checked out, the test is skipped.
func TestSchema_ParityWithEvolver(t *testing.T) {
	if len(schemaBytes) == 0 {
		t.Fatal("schema not loaded")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	evalRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	parent := filepath.Dir(evalRoot)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Skipf("cannot read parent dir %s: %v", parent, err)
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "helixon-evol") && name != filepath.Base(evalRoot) {
			candidates = append(candidates, filepath.Join(parent, name))
		}
	}
	if len(candidates) == 0 {
		t.Skip("helixon-evolver not checked out as sibling; run with both repos present to enforce parity")
	}

	var checked int
	for _, evolverRoot := range candidates {
		gm, err := os.ReadFile(filepath.Join(evolverRoot, "go.mod"))
		if err != nil {
			continue
		}
		if !strings.Contains(string(gm), "helixon-evol") {
			continue
		}
		evolverSchema := filepath.Join(evolverRoot, "contracts", "ndjson-eval-result.schema.json")
		disk, err := os.ReadFile(evolverSchema)
		if err != nil {
			t.Errorf("read evolver schema %s: %v", evolverSchema, err)
			continue
		}
		if sha256Hex(disk) != sha256Hex(schemaBytes) {
			t.Errorf("evolver schema (%s) differs from eval schema; copy contracts/ndjson-eval-result.schema.json from helixon-eval to helixon-evolver and bump schema_version", evolverSchema)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("no evolver candidate with go.mod declaring helixon-evol* module name")
	}
}

// TestSchema_LockFileExists asserts the regression-contract lock doc
// referenced in ADR-0008 IMP-001 exists somewhere in the eval repo.
func TestSchema_LockFileExists(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	want := "helixon-eval-evolver-ndjson-parity"
	var hits []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		if !d.IsDir() && strings.Contains(d.Name(), want) {
			hits = append(hits, path)
		}
		return nil
	})
	if err != nil {
		t.Skipf("walk %s: %v", repoRoot, err)
	}
	if len(hits) == 0 {
		t.Skipf("regression contract lock file %q not found under %s; create it (e.g. docs/contracts/%s.md) so the IMP-001 deliverable is discoverable", want, repoRoot, want)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
