// Package idempotency — pilot LLM-call idempotency cache (v18688-3).
//
// Wraps any llmclient.Client.Chat call with a deterministic JobID
// key derived from (prompt-fingerprint, model, backend). Repeated
// calls with the same logical inputs short-circuit and return the
// cached response without an upstream round-trip — protecting the
// pilot against retries that would otherwise double-bill.
//
// Plan §3 mandates:
//   - JobID for (prompt, model, backend) is SHA-256 prefix (8 hex).
//   - Cache hit returns cached ChatResponse + cost flag IsReplay=true.
//   - Cache miss performs real Chat() and stores result.
//   - Cache errors MUST NOT mask upstream errors.
//   - Bounded memory: TTL + max-entries eviction.
//
// Design constraints (per harness-engineering-defaults.mdc):
//   - sync.Mutex protects the map; per-call critical section is short.
//   - No goroutine leakage (the cache itself is goroutine-free; the
//     caller drives concurrency).
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/llmclient"
)

// Cache is a thread-safe in-memory idempotency layer for LLM calls.
// Zero-value is unusable; construct via NewCache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	maxSize int
	ttl     time.Duration
	now     func() time.Time // injectable for tests
	stat    Stats
}

type entry struct {
	resp     *llmclient.ChatResponse
	storedAt time.Time
}

// Stats is a read-only view of cache counters.
type Stats struct {
	Hits      int
	Misses    int
	Evictions int
}

// NewCache returns a Cache with TTL and max-entries. ttl=0 means
// no expiry (entries live forever or until evicted by size cap);
// maxSize=0 means unbounded (use with care).
func NewCache(ttl time.Duration, maxSize int) *Cache {
	return &Cache{
		entries: make(map[string]entry),
		maxSize: maxSize,
		ttl:     ttl,
		now:     time.Now,
	}
}

// SetClock overrides the wall-clock for tests. Pass nil to reset.
func (c *Cache) SetClock(fn func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		fn = time.Now
	}
	c.now = fn
}

// JobID returns a deterministic 8-hex-char SHA-256 prefix over the
// logical inputs (prompt-fingerprint, model, backend). The same
// logical call MUST always produce the same JobID.
//
// `promptFingerprint` is whatever the caller uses to fingerprint
// the conversation (typically a SHA-256 over the messages JSON).
func JobID(promptFingerprint, model string, backend llmclient.Backend) string {
	h := sha256.Sum256([]byte(promptFingerprint + "|" + model + "|" + string(backend)))
	return hex.EncodeToString(h[:4])
}

// Lookup returns a cached response if one exists and is still fresh.
// `ok` indicates whether a cache hit occurred. Hits increment the
// Hits stat counter.
func (c *Cache) Lookup(jobID string) (resp *llmclient.ChatResponse, ok bool) {
	c.mu.RLock()
	e, found := c.entries[jobID]
	if !found {
		c.mu.RUnlock()
		return nil, false
	}
	if c.ttl > 0 && c.now().Sub(e.storedAt) > c.ttl {
		c.mu.RUnlock()
		return nil, false
	}
	resp = e.resp
	c.mu.RUnlock()
	c.mu.Lock()
	c.stat.Hits++
	c.mu.Unlock()
	return resp, true
}

// Store inserts (or overwrites) an entry. It enforces the maxSize
// cap by evicting the oldest entry on overflow.
func (c *Cache) Store(jobID string, resp *llmclient.ChatResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxSize > 0 && len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}
	c.entries[jobID] = entry{resp: resp, storedAt: c.now()}
}

// evictOldestLocked removes the entry with the earliest storedAt.
// Caller must hold c.mu (write).
func (c *Cache) evictOldestLocked() {
	var oldestID string
	var oldestTime time.Time
	first := true
	for id, e := range c.entries {
		if first || e.storedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = e.storedAt
			first = false
		}
	}
	if oldestID != "" {
		delete(c.entries, oldestID)
		c.stat.Evictions++
	}
}

// Size returns the current entry count.
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// GetStats returns a snapshot of the cache counters.
func (c *Cache) GetStats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stat
}

// ErrIdempotentReplay is returned by CachedChat when the cache hit
// is served; callers can branch on this sentinel to skip post-processing
// (e.g. logging the request as a fresh call).
var ErrIdempotentReplay = errors.New("idempotent replay (cache hit)")

// CachedChat wraps llmclient.Client.Chat with idempotency. On a cache
// hit it returns (cached, ErrIdempotentReplay) WITHOUT dialing upstream;
// on miss it calls Chat() and stores the result. The jobID is computed
// from (prompt-fingerprint, model, backend).
//
// The prompt-fingerprint is supplied by the caller (typically a
// SHA-256 over `req.Messages` JSON).
func CachedChat(ctx context.Context, c *llmclient.Client, cache *Cache, promptFingerprint string, req llmclient.ChatRequest) (*llmclient.ChatResponse, error) {
	if c == nil {
		return nil, errors.New("llmclient.Client is nil")
	}
	if cache == nil {
		// No cache configured: pass through.
		return c.Chat(ctx, req)
	}

	jobID := JobID(promptFingerprint, req.Model, c.Backend())

	if cached, ok := cache.Lookup(jobID); ok {
		// Lookup already incremented Hits. Return replay.
		return cached, ErrIdempotentReplay
	}

	cache.mu.Lock()
	cache.stat.Misses++
	cache.mu.Unlock()

	resp, err := c.Chat(ctx, req)
	if err != nil {
		// Do NOT cache errors — a transient 5xx should not poison
		// the cache for the next retry.
		return nil, err
	}
	cache.Store(jobID, resp)
	return resp, nil
}

// FingerprintMessages produces a stable SHA-256 hex digest over a slice
// of chat messages. Used as the promptFingerprint arg to CachedChat.
func FingerprintMessages(msgs []llmclient.Message) string {
	h := sha256.New()
	for _, m := range msgs {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0x1f}) // unit separator — guard against (role,content) collisions
	}
	return hex.EncodeToString(h.Sum(nil))
}
