package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// idemSecret is a process-local HMAC key. The secret is intentionally
// not persisted — it scopes idempotency to the current process + the
// JSONL store, so a fresh run starts with a fresh key namespace.
var idemSecret = []byte(fmt.Sprintf("helixon-eval-r6-%d", time.Now().UnixNano()))

// IdempotencyBucketSeconds is the size of the timestamp bucket in seconds.
// 5 minutes (300s) matches the spec.
const IdempotencyBucketSeconds = 300

// IdempotencyKey derives a deterministic HMAC-SHA256 key from
// (model, prompt, timestamp_bucket). Same inputs → same key.
func IdempotencyKey(model, prompt string, bucket int64) string {
	mac := hmac.New(sha256.New, idemSecret)
	mac.Write([]byte(model))
	mac.Write([]byte{0})
	mac.Write([]byte(prompt))
	mac.Write([]byte{0})
	fmt.Fprintf(mac, "%d", bucket)
	return hex.EncodeToString(mac.Sum(nil))
}

// IdempotencyStore tracks seen keys in a JSONL file. The store is
// process-safe via an internal mutex; concurrent Record/Seen across
// processes is best-effort (last writer wins on append).
type IdempotencyStore struct {
	mu   sync.Mutex
	path string
	seen map[string]bool
}

// NewIdempotencyStore opens (creating if missing) the store at path.
func NewIdempotencyStore(path string) (*IdempotencyStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &IdempotencyStore{path: path, seen: make(map[string]bool)}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var rec struct {
			Key string `json:"key"`
		}
		if err := dec.Decode(&rec); err != nil {
			break
		}
		s.seen[rec.Key] = true
	}
	return s, nil
}

// Seen reports whether the given key has been recorded.
func (s *IdempotencyStore) Seen(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[key]
}

// Record appends the key to the JSONL store (process-safe).
func (s *IdempotencyStore) Record(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[key] {
		return nil
	}
	s.seen[key] = true
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(map[string]string{"key": key, "ts": time.Now().Format(time.RFC3339)})
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}
