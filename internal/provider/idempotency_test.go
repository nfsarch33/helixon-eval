package provider

import (
	"path/filepath"
	"testing"
)

func TestIdempotencyKey_DeterministicForSameInputs(t *testing.T) {
	k1 := IdempotencyKey("MiniMax-M3", "hello world", 1730870400)
	k2 := IdempotencyKey("MiniMax-M3", "hello world", 1730870400)
	if k1 != k2 {
		t.Errorf("non-deterministic: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("expected 64 hex chars (sha256), got %d", len(k1))
	}
}

func TestIdempotencyKey_DifferentForDifferentPrompts(t *testing.T) {
	k1 := IdempotencyKey("MiniMax-M3", "hello", 1730870400)
	k2 := IdempotencyKey("MiniMax-M3", "world", 1730870400)
	if k1 == k2 {
		t.Errorf("collision for different prompts")
	}
}

func TestIdempotencyKey_DifferentBuckets(t *testing.T) {
	bucketSize := int64(300) // 5 minutes
	t0 := int64(1730870400)
	k1 := IdempotencyKey("MiniMax-M3", "x", t0/bucketSize)
	k2 := IdempotencyKey("MiniMax-M3", "x", (t0+bucketSize)/bucketSize)
	if k1 == k2 {
		t.Errorf("same key across bucket boundary")
	}
}

func TestIdempotencyStore_SeenReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIdempotencyStore(filepath.Join(dir, "idem.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	key := IdempotencyKey("MiniMax-M3", "p", 1)
	if store.Seen(key) {
		t.Fatalf("expected unseen")
	}
	if err := store.Record(key); err != nil {
		t.Fatal(err)
	}
	if !store.Seen(key) {
		t.Fatalf("expected seen after record")
	}
}

func TestIdempotencyStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idem.jsonl")
	s1, err := NewIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	key := IdempotencyKey("MiniMax-M3", "p", 2)
	if err := s1.Record(key); err != nil {
		t.Fatal(err)
	}
	s2, err := NewIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Seen(key) {
		t.Fatalf("expected seen after reload")
	}
}
