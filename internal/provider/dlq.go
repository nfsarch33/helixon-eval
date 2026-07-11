package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DLQEntry is one dead-letter record, persisted as NDJSON.
type DLQEntry struct {
	TS         string `json:"ts"`
	Model      string `json:"model"`
	PromptHash string `json:"prompt_hash"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error"`
}

// DLQ is the dead-letter queue (append-only NDJSON file).
type DLQ struct {
	mu   sync.Mutex
	path string
}

// NewDLQ creates a DLQ at the given path. The parent directory is
// created if missing.
func NewDLQ(path string) (*DLQ, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &DLQ{path: path}, nil
}

// DefaultDLQPath returns ~/logs/helixon-eval/dlq.ndjson.
func DefaultDLQPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "logs", "helixon-eval", "dlq.ndjson")
}

// Append writes one DLQ entry as NDJSON.
func (q *DLQ) Append(e DLQEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.TS == "" {
		e.TS = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(q.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
