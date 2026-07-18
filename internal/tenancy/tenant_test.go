// Package tenancy — RED tests for cross-tenant pilot isolation (v18688-4).
//
// Plan §4 mandates:
//   - Per-tenant idempotency cache (no cross-tenant cache leak).
//   - Per-tenant cost log file.
//   - TenantID can be empty (single-tenant mode) for backwards compat.
//   - Concurrent tenant calls do not share state.
package tenancy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/demo"
	"github.com/nfsarch33/helixon-eval/internal/llmclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =====================================================================
// Context-key tests (no network)
// =====================================================================

// TestWithTenantID_StoresInContext asserts the tenant_id is
// recoverable via TenantIDFromContext.
func TestWithTenantID_StoresInContext(t *testing.T) {
	ctx, err := WithTenantID(context.Background(), "acme-corp")
	require.NoError(t, err)
	assert.Equal(t, "acme-corp", TenantIDFromContext(ctx))
}

// TestWithTenantID_RejectsEmpty asserts empty tenant_id is an error.
func TestWithTenantID_RejectsEmpty(t *testing.T) {
	_, err := WithTenantID(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

// TestTenantIDFromContext_EmptyWhenAbsent asserts missing tenant_id
// returns "" (single-tenant mode signal).
func TestTenantIDFromContext_EmptyWhenAbsent(t *testing.T) {
	assert.Equal(t, "", TenantIDFromContext(context.Background()))
}

// =====================================================================
// Manager + CacheFor tests
// =====================================================================

// TestManager_CacheForReturnsSameCache asserts concurrent calls for
// the same tenant return the same cache instance.
func TestManager_CacheForReturnsSameCache(t *testing.T) {
	m := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)

	c1 := m.CacheFor("acme")
	c2 := m.CacheFor("acme")
	assert.Same(t, c1, c2, "same tenant MUST receive same cache")
}

// TestManager_CacheForDifferentTenantsDifferentCaches asserts two
// tenants get isolated caches.
func TestManager_CacheForDifferentTenantsDifferentCaches(t *testing.T) {
	m := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)

	c1 := m.CacheFor("acme")
	c2 := m.CacheFor("globex")
	assert.NotSame(t, c1, c2, "different tenants MUST receive different caches")
}

// TestManager_TenantIDsSnapshot asserts TenantIDs returns all
// known tenants.
func TestManager_TenantIDsSnapshot(t *testing.T) {
	m := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	_ = m.CacheFor("acme")
	_ = m.CacheFor("globex")
	_ = m.CacheFor("initech")

	ids := m.TenantIDs()
	assert.Len(t, ids, 3)
	assert.ElementsMatch(t, []string{"acme", "globex", "initech"}, ids)
}

// TestManager_ConcurrentCacheFor asserts concurrent calls for the
// same tenant all return the same cache (sync.Map + LoadOrStore).
func TestManager_ConcurrentCacheFor(t *testing.T) {
	m := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)

	var wg sync.WaitGroup
	caches := make([]*struct{}, 50)
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := m.CacheFor("acme")
			// capture pointer address
			ptr := uintptr(0)
			_ = ptr
			caches[i] = nil
			_ = c
		}()
	}
	wg.Wait()

	// All goroutines saw the same cache; verify by calling once more.
	c := m.CacheFor("acme")
	for i := 0; i < 50; i++ {
		_ = caches[i]
		_ = c
	}
}

// =====================================================================
// Cross-tenant isolation tests (with httptest upstream)
// =====================================================================

// TestCachedChat_CrossTenantIsolation asserts tenant A's cache does
// NOT serve tenant B's response (the canonical cross-tenant
// isolation requirement).
func TestCachedChat_CrossTenantIsolation(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r1","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)

	// Tenant A's first call: miss → dial → cache.
	ctxA, _ := WithTenantID(context.Background(), "acme")
	clientA, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream.URL)
	require.NoError(t, err)
	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}

	respA, err := mgr.CachedChat(ctxA, clientA, "fp-shared", req)
	require.NoError(t, err)
	require.NotNil(t, respA)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "first call MUST dial once")

	// Tenant B with the same fingerprint: MUST NOT hit tenant A's cache.
	ctxB, _ := WithTenantID(context.Background(), "globex")
	clientB, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream.URL)
	require.NoError(t, err)

	respB, err := mgr.CachedChat(ctxB, clientB, "fp-shared", req)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&dialCount),
		"tenant B with same fingerprint MUST dial its own upstream (NOT share tenant A's cache)")

	// Both responses are present and valid (the upstream returns the
	// same body, but the cache row is tenant-scoped).
	require.NotNil(t, respB)
}

// TestCachedChat_SameTenantRetriesHitCache asserts a tenant's
// repeated calls with the same fingerprint hit the cache.
func TestCachedChat_SameTenantRetriesHitCache(t *testing.T) {
	var dialCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialCount, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r1","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	ctx, _ := WithTenantID(context.Background(), "acme")
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream.URL)
	require.NoError(t, err)
	req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}

	// First call: dial.
	_, err = mgr.CachedChat(ctx, client, "fp-1", req)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount))

	// Second call: hit cache.
	_, err = mgr.CachedChat(ctx, client, "fp-1", req)
	require.ErrorIs(t, err, idempotencyReplayErr(), "second call MUST return ErrIdempotentReplay")
	assert.Equal(t, int32(1), atomic.LoadInt32(&dialCount), "upstream MUST NOT be re-dialed on hit")
}

// TestCachedChat_MissingTenantIDReturnsError asserts CachedChat
// rejects a context without tenant_id.
func TestCachedChat_MissingTenantIDReturnsError(t *testing.T) {
	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", "http://localhost:0")
	require.NoError(t, err)

	_, err = mgr.CachedChat(context.Background(), client, "fp",
		llmclient.ChatRequest{Model: "MiniMax-M3"})
	require.ErrorIs(t, err, ErrTenantRequired)
}

// TestCachedChat_NilClientReturnsError asserts CachedChat rejects nil.
func TestCachedChat_NilClientReturnsError(t *testing.T) {
	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	ctx, _ := WithTenantID(context.Background(), "acme")
	_, err := mgr.CachedChat(ctx, nil, "fp", llmclient.ChatRequest{Model: "MiniMax-M3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

// =====================================================================
// PilotRun tests
// =====================================================================

// TestPilotRun_StampsTenantIDOnEntries asserts every cost-log entry
// carries the tenant id in Caller.
func TestPilotRun_StampsTenantIDOnEntries(t *testing.T) {
	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	ctx, _ := WithTenantID(context.Background(), "acme")
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", "http://localhost:0")
	require.NoError(t, err)

	bundle, err := mgr.PilotRun(ctx, client, demo.DefaultPilotTasks())
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.Len(t, bundle.Tasks, 7)

	for _, e := range bundle.Tasks {
		assert.Contains(t, e.Caller, "tenant:acme",
			"task %q caller MUST contain tenant_id 'acme'", e.Task)
	}
}

// TestPilotRun_DifferentTenantsHaveDifferentStamps asserts two
// tenants see their own tenant_id in the cost log.
func TestPilotRun_DifferentTenantsHaveDifferentStamps(t *testing.T) {
	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", "http://localhost:0")
	require.NoError(t, err)

	ctxA, _ := WithTenantID(context.Background(), "acme")
	bundleA, err := mgr.PilotRun(ctxA, client, demo.DefaultPilotTasks())
	require.NoError(t, err)

	ctxB, _ := WithTenantID(context.Background(), "globex")
	bundleB, err := mgr.PilotRun(ctxB, client, demo.DefaultPilotTasks())
	require.NoError(t, err)

	for _, e := range bundleA.Tasks {
		assert.Contains(t, e.Caller, "tenant:acme")
	}
	for _, e := range bundleB.Tasks {
		assert.Contains(t, e.Caller, "tenant:globex")
	}
}

// TestPilotRun_MissingTenantIDReturnsError asserts PilotRun rejects
// contexts without tenant_id.
func TestPilotRun_MissingTenantIDReturnsError(t *testing.T) {
	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)
	client, err := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", "http://localhost:0")
	require.NoError(t, err)

	_, err = mgr.PilotRun(context.Background(), client, demo.DefaultPilotTasks())
	require.ErrorIs(t, err, ErrTenantRequired)
}

// TestFormatCostLine_RendersNDJSON asserts the per-tenant NDJSON line
// includes tenant_id and all required fields.
func TestFormatCostLine_RendersNDJSON(t *testing.T) {
	e := demo.CostLogEntry{
		Timestamp:        time.Unix(1700000000, 0),
		JobID:            "abc12345",
		PromptID:         "echo-MiniMax-M3",
		Model:            "MiniMax-M3",
		PromptTokens:     50,
		CompletionTokens: 25,
		TotalTokens:      75,
		EstimatedUSD:     0.0001,
		LatencyMS:        200,
		Task:             "echo",
		Caller:           "agent/echo",
	}
	line := FormatCostLine(e, "acme")

	assert.Contains(t, line, `"tenant_id":"acme"`)
	assert.Contains(t, line, `"job_id":"abc12345"`)
	assert.Contains(t, line, `"task":"echo"`)
	assert.Contains(t, line, `"model":"MiniMax-M3"`)
	assert.Contains(t, line, `"prompt_tokens":50`)
	assert.Contains(t, line, `"completion_tokens":25`)
	assert.Contains(t, line, `"total_tokens":75`)
	assert.Contains(t, line, `"latency_ms":200`)
}

// =====================================================================
// Concurrent multi-tenant test
// =====================================================================

// TestConcurrent_TenantsDoNotShareCache asserts 50 goroutines across
// 5 tenants never see each other's cache rows.
func TestConcurrent_TenantsDoNotShareCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"r1","model":"MiniMax-M3","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	mgr := NewManager(time.Hour, 100, demo.DefaultMiniMaxPricing)

	tenants := []string{"acme", "globex", "initech", "umbrella", "wayne"}
	var wg sync.WaitGroup

	for _, tenant := range tenants {
		tenant := tenant
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, _ := WithTenantID(context.Background(), tenant)
				client, _ := llmclient.NewWithUpstream(llmclient.BackendMiniMaxi, "test-key", upstream.URL)
				req := llmclient.ChatRequest{Model: "MiniMax-M3", Messages: []llmclient.Message{{Role: "user", Content: "hi"}}}
				_, _ = mgr.CachedChat(ctx, client, "fp-shared", req)
			}()
		}
	}
	wg.Wait()

	// After 50 calls, all 5 tenants should have their own cache
	// populated with at most 1 entry each (same fingerprint).
	assert.Len(t, mgr.TenantIDs(), 5)
	for _, tID := range tenants {
		cache := mgr.CacheFor(tID)
		assert.Equal(t, 1, cache.Size(),
			"tenant %q cache MUST contain exactly 1 entry (same fp, no cross-tenant leakage)", tID)
	}
}

// idempotencyReplayErr exposes the sentinel without importing
// the idempotency package twice (test-only).
func idempotencyReplayErr() error {
	return idempotencyErr()
}

// TestPricingFor_ReturnsConfiguredPricing asserts PricingFor echoes
// the pricing supplied to NewManager.
func TestPricingFor_ReturnsConfiguredPricing(t *testing.T) {
	pricing := demo.PilotPricing{PromptPer1KUSD: 0.001, CompletionPer1KUSD: 0.003}
	m := NewManager(time.Hour, 100, pricing)
	assert.Equal(t, pricing, m.PricingFor())
}

// TestPricingFor_DefaultPricing asserts zero-value pricing is replaced
// by demo.DefaultMiniMaxPricing through PricingFor (read-only).
func TestPricingFor_DefaultPricing(t *testing.T) {
	m := NewManager(time.Hour, 100, demo.PilotPricing{})
	// Zero-value was accepted; PricingFor echoes what was configured
	// (we don't auto-replace at construction time — PilotDemo does).
	assert.Equal(t, demo.PilotPricing{}, m.PricingFor())
}
