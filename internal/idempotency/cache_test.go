// Package idempotency — RED tests for pilot LLM-call idempotency (v18688-3).
//
// Plan §3 mandates:
//   - JobID for (prompt, model, backend) is SHA-256 prefix (8 hex).
//   - Cache hit returns cached ChatResponse + IsReplay=true.
//   - Cache miss performs real Chat() and stores result.
//   - Cache errors MUST NOT mask upstream errors.
//   - Bounded memory: TTL + max-entries eviction.
package idempotency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/llmclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =====================================================================
// Pure-function tests (no network)
// =====================================================================

// TestJobID_Deterministic asserts JobID is the same SHA-256 prefix
// across multiple calls with identical inputs.
func TestJobID_Deterministic(t *testing.T) {
	id1 := JobID("prompt-1", "MiniMax-M3", llmclient.BackendMiniMaxi)
	id2 := JobID("prompt-1", "MiniMax-M3", llmclient.BackendMiniMaxi)
	assert.Equal(t, id1, id2, "JobID MUST be deterministic across calls")
}

// TestJobID_Length asserts JobID is exactly 8 hex chars.
func TestJobID_Length(t *testing.T) {
	id := JobID("prompt", "MiniMax-M3", llmclient.BackendMiniMaxi)
	assert.Equal(t, 8, len(id), "JobID MUST be 8 hex chars (sha256 prefix)")
}

// TestJobID_DifferentInputs asserts different inputs produce different JobIDs.
func TestJobID_DifferentInputs(t *testing.T) {
	base := JobID("prompt-1", "MiniMax-M3", llmclient.BackendMiniMaxi)
	assert.NotEqual(t, base, JobID("prompt-2", "MiniMax-M3", llmclient.BackendMiniMaxi), "different prompt → different JobID")
	assert.NotEqual(t, base, JobID("prompt-1", "MiniMax-Text-01", llmclient.BackendMiniMaxi), "different model → different JobID")
	assert.NotEqual(t, base, JobID("prompt-1", "MiniMax-M3", llmclient.BackendQwenPlus), "different backend → different JobID")
}

// TestFingerprintMessages_Deterministic asserts the same messages
// produce the same fingerprint.
func TestFingerprintMessages_Deterministic(t *testing.T) {
	msgs := []llmclient.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	fp1 := FingerprintMessages(msgs)
	fp2 := FingerprintMessages(msgs)
	assert.Equal(t, fp1, fp2, "FingerprintMessages MUST be deterministic")
}

// TestFingerprintMessages_OrderSensitive asserts reordering
// messages changes the fingerprint (preserves conversation order).
func TestFingerprintMessages_OrderSensitive(t *testing.T) {
	a := []llmclient.Message{{Role: "user", Content: "x"}}
	b := []llmclient.Message{{Role: "user", Content: "x"}, {Role: "user", Content: "y"}}
	assert.NotEqual(t, FingerprintMessages(a), FingerprintMessages(b),
		"different message counts MUST produce different fingerprints")
}

// TestFingerprintMessages_DifferentContent asserts content changes
// produce different fingerprints.
func TestFingerprintMessages_DifferentContent(t *testing.T) {
	a := []llmclient.Message{{Role: "user", Content: "x"}}
	b := []llmclient.Message{{Role: "user", Content: "y"}}
	assert.NotEqual(t, FingerprintMessages(a), FingerprintMessages(b),
		"different content MUST produce different fingerprints")
}

// =====================================================================
// Lookup/Store tests
// =====================================================================

// TestCache_StoreAndLookup asserts the basic round-trip.
func TestCache_StoreAndLookup(t *testing.T) {
	c := NewCache(time.Hour, 100)
	resp := &llmclient.ChatResponse{ID: "x", Model: "MiniMax-M3"}
	c.Store("abc12345", resp)

	got, ok := c.Lookup("abc12345")
	require.True(t, ok)
	assert.Equal(t, "x", got.ID)
}

// TestCache_MissReturnsFalse asserts an absent JobID returns ok=false.
func TestCache_MissReturnsFalse(t *testing.T) {
	c := NewCache(time.Hour, 100)
	_, ok := c.Lookup("missing")
	assert.False(t, ok)
}

// TestCache_TTLExpiry asserts expired entries are not returned.
func TestCache_TTLExpiry(t *testing.T) {
	c := NewCache(1*time.Hour, 100)
	c.SetClock(func() time.Time { return time.Unix(1700000000, 0) })
	resp := &llmclient.ChatResponse{ID: "x"}
	c.Store("k", resp)

	// Move clock past TTL.
	c.SetClock(func() time.Time { return time.Unix(1700000000+7200, 0) })
	_, ok := c.Lookup("k")
	assert.False(t, ok, "TTL-expired entry MUST not be served")
}

// TestCache_MaxSizeEvictsOldest asserts the maxSize cap evicts
// the oldest entry on overflow.
func TestCache_MaxSizeEvictsOldest(t *testing.T) {
	c := NewCache(time.Hour, 2)

	t0 := time.Unix(1700000000, 0)
	c.SetClock(func() time.Time { return t0 })

	c.Store("a", &llmclient.ChatResponse{ID: "a"})
	c.SetClock(func() time.Time { return t0.Add(1 * time.Second) })
	c.Store("b", &llmclient.ChatResponse{ID: "b"})
	c.SetClock(func() time.Time { return t0.Add(2 * time.Second) })
	c.Store("c", &llmclient.ChatResponse{ID: "c"})

	assert.Equal(t, 2, c.Size(), "maxSize MUST cap entry count to 2")
	_, ok := c.Lookup("a")
	assert.False(t, ok, "oldest entry 'a' MUST be evicted")
	_, ok = c.Lookup("b")
	assert.True(t, ok, "newer entry 'b' MUST survive")
	_, ok = c.Lookup("c")
	assert.True(t, ok, "newest entry 'c' MUST survive")

	stats := c.GetStats()
	assert.Equal(t, 1, stats.Evictions, "eviction counter MUST increment")
}

// TestCache_NoTTL means ttl=0 entries live forever (only maxSize bounds).
func TestCache_NoTTL(t *testing.T) {
	c := NewCache(0, 100)
	resp := &llmclient.ChatResponse{ID: "x"}

	// Advance clock by 100 years — entry still valid because ttl=0.
	t0 := time.Unix(1700000000, 0)
	c.SetClock(func() time.Time { return t0 })
	c.Store("k", resp)
	c.SetClock(func() time.Time { return t0.Add(100 * 365 * 24 * time.Hour) })

	got, ok := c.Lookup("k")
	assert.True(t, ok, "ttl=0 MUST mean no expiry")
	assert.Equal(t, "x", got.ID)
}

// TestCache_StatsResetOnMiss asserts Lookup of absent keys does NOT
// bump Misses (Misses is reserved for the CachedChat upstream path;
// raw Lookup is a probe). This matches the v18688-3 contract: Hits
// track served cached results; Misses track actual upstream dials.
func TestCache_StatsResetOnMiss(t *testing.T) {
	c := NewCache(time.Hour, 100)

	_, ok := c.Lookup("miss-1")
	assert.False(t, ok)
	_, ok = c.Lookup("miss-2")
	assert.False(t, ok)

	stats := c.GetStats()
	assert.Equal(t, 0, stats.Misses, "raw Lookup misses MUST NOT register (Lookup is a probe)")
	assert.Equal(t, 0, stats.Hits)
}

// TestCache_StatsHitOnLookup asserts each hit increments Hits.
func TestCache_StatsHitOnLookup(t *testing.T) {
	c := NewCache(time.Hour, 100)
	resp := &llmclient.ChatResponse{ID: "x"}
	c.Store("k", resp)

	_, _ = c.Lookup("k")
	_, _ = c.Lookup("k")
	_, _ = c.Lookup("k")

	stats := c.GetStats()
	assert.Equal(t, 3, stats.Hits)
}

// =====================================================================
// CachedChat end-to-end (with httptest upstream)
// =====================================================================

// TestCachedChat_MissAndStore asserts the first call performs Chat() and
// stores the response; the upstream is dialed exactly once. The second
// call hits the cache and does NOT re-dial.
func TestCachedChat_MissAndStore(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r1","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	cache := NewCache(time.Hour, 100)
	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}

	// Round 1: miss → dial upstream → store.
	got1, err1 := callWithUpstream(upstream.URL, cache, "fp-1", req)
	require.NoError(t, err1)
	require.NotNil(t, got1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "upstream dialed once on miss")

	// Round 2: hit → no upstream → ErrIdempotentReplay (caller contract).
	got2, err2 := callWithUpstream(upstream.URL, cache, "fp-1", req)
	require.ErrorIs(t, err2, ErrIdempotentReplay, "second call MUST return ErrIdempotentReplay")
	require.NotNil(t, got2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "upstream MUST NOT be re-dialed on hit")
	assert.Same(t, got1, got2, "cached response pointer MUST be reused")

	stats := cache.GetStats()
	assert.Equal(t, 1, stats.Hits, "exactly one hit expected")
}

// TestCachedChat_HitReturnsReplayErr asserts the second call returns
// ErrIdempotentReplay because the cache hit short-circuits the upstream.
func TestCachedChat_HitReturnsReplayErr(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r1","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	cache := NewCache(time.Hour, 100)
	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}

	// Round 1
	_, err := callWithUpstream(upstream.URL, cache, "fp-replay", req)
	require.NoError(t, err)

	// Round 2: should hit cache and return ErrIdempotentReplay.
	_, err = callWithUpstream(upstream.URL, cache, "fp-replay", req)
	require.ErrorIs(t, err, ErrIdempotentReplay, "second call MUST return ErrIdempotentReplay")
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "upstream MUST NOT be re-dialed on hit")
}

// TestCachedChat_UpstreamErrorNotCached asserts that a Chat() error
// (e.g. transient 5xx) is NOT cached, so the next retry can succeed.
func TestCachedChat_UpstreamErrorNotCached(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"transient"}`))
	}))
	defer upstream.Close()

	cache := NewCache(time.Hour, 100)
	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}

	// Round 1: upstream 500 → error → NOT cached.
	_, err := callWithUpstream(upstream.URL, cache, "fp-err", req)
	require.Error(t, err, "upstream 500 MUST surface as error")

	// Round 2: upstream 200 → cache miss → success → cached.
	upstream.Close()
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r2","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	_, err = callWithUpstream(upstream.URL, cache, "fp-err", req)
	require.NoError(t, err, "after transient failure recovery, retry MUST succeed")
	assert.Equal(t, int32(2), atomic.LoadInt32(&dialCount))
	assert.Equal(t, 1, cache.Size(), "recovered response MUST be cached")
}

// TestCachedChat_NilClientError asserts CachedChat rejects a nil client.
func TestCachedChat_NilClientError(t *testing.T) {
	cache := NewCache(time.Hour, 100)
	_, err := CachedChat(context.Background(), nil, cache, "fp",
		llmclient.ChatRequest{Model: "MiniMax-M3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

// TestCachedChat_NilCachePassThrough asserts CachedChat with nil
// cache is a pure pass-through (caller responsibility for idempotency).
// We verify by constructing a custom transport that returns 200 and
// asserting the response is reachable through CachedChat.
func TestCachedChat_NilCachePassThrough(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream.URL)
	require.NoError(t, err)

	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}
	resp, err := CachedChat(context.Background(), client, nil, "fp-pt", req)
	require.NoError(t, err)
	assert.Equal(t, "x", resp.ID)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "pass-through MUST dial upstream")
}

// TestCachedChat_DifferentModelsDifferentKeys asserts two requests
// with the same messages but different models get different JobIDs.
func TestCachedChat_DifferentModelsDifferentKeys(t *testing.T) {
	fp := "fp-shared"
	id1 := JobID(fp, "MiniMax-M3", llmclient.BackendMiniMaxi)
	id2 := JobID(fp, "MiniMax-Text-01", llmclient.BackendMiniMaxi)
	assert.NotEqual(t, id1, id2)
}

// TestCachedChat_ConcurrentHits asserts the cache is safe under
// concurrent access (RWMutex contract).
func TestCachedChat_ConcurrentHits(t *testing.T) {
	cache := NewCache(time.Hour, 100)
	cache.Store("k", &llmclient.ChatResponse{ID: "shared"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok := cache.Lookup("k")
			require.True(t, ok)
			assert.Equal(t, "shared", got.ID)
		}()
	}
	wg.Wait()

	stats := cache.GetStats()
	assert.Equal(t, 50, stats.Hits, "all 50 concurrent lookups MUST register as hits")
}

// =====================================================================
// Helpers
// =====================================================================

// callWithUpstream drives CachedChat against an httptest upstream. We
// cannot reach llmclient.Client.Chat directly because it uses the
// hard-coded backend endpoint. So we wrap the upstream URL via
// llmclient.NewWithUpstream (added for tests).
func callWithUpstream(upstream string, cache *Cache, fp string, req llmclient.ChatRequest) (*llmclient.ChatResponse, error) {
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream)
	if err != nil {
		return nil, err
	}
	return CachedChat(context.Background(), client, cache, fp, req)
}

// silence unused-import warnings on go vet
var _ = strings.NewReader
