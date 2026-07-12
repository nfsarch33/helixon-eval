package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDLQ_AppendsNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dlq.ndjson")
	q, err := NewDLQ(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Append(DLQEntry{Model: "MiniMax-M3", PromptHash: "abc", Attempts: 3, LastError: "HTTP 503"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("dlq not newline-terminated")
	}
	var entry DLQEntry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry.Model != "MiniMax-M3" || entry.Attempts != 3 {
		t.Errorf("entry=%+v", entry)
	}
}

func TestDLQ_DeadLetterAfterMaxRetries(t *testing.T) {
	dir := t.TempDir()
	dlqPath := filepath.Join(dir, "dlq.ndjson")
	idemPath := filepath.Join(dir, "idem.jsonl")
	q, _ := NewDLQ(dlqPath)
	idem, _ := NewIdempotencyStore(idemPath)
	p := DefaultRetryPolicy()

	_ = idem.Record(IdempotencyKey("MiniMax-M3", "p1", 0))
	if !idem.Seen(IdempotencyKey("MiniMax-M3", "p1", 0)) {
		t.Fatalf("expected idempotency key marked seen before dead-lettering")
	}

	_, attempts, err := CallWithRetry(context.Background(), p, func(ctx context.Context) (Response, error) {
		return Response{}, errors.New("HTTP 503 always down")
	})
	if err == nil {
		t.Fatal("expected error after retries")
	}
	if attempts != p.MaxAttempts {
		t.Errorf("attempts=%d want %d", attempts, p.MaxAttempts)
	}
	if err := q.Append(DLQEntry{
		Model: "MiniMax-M3", PromptHash: "p1", Attempts: attempts, LastError: err.Error(),
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dlqPath)
	var entry DLQEntry
	_ = json.Unmarshal(data[:len(data)-1], &entry)
	if entry.Model != "MiniMax-M3" || entry.PromptHash != "p1" {
		t.Errorf("dlq entry=%+v", entry)
	}
}
