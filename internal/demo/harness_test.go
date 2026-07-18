// RED tests for v18692-5 real-models harness.
//
// Coverage targets (per harness-engineering-defaults.mdc):
//
//   - ParseProvider canonicalisation & aliases
//   - Provider.Model/.Backend/.EnvKey round-trip
//   - RunMatrix dry-run produces one row per provider with status=ok
//   - RenderMarkdown includes provider matrix, ranking, and per-task table
//   - AppendNDJSON creates/extends file (offline check)
//   - PricingFor returns realistic per-provider rates
//   - EnvLookup / ResolveAPIKeysFor precedence: override > env
//   - RunMatrix rejects empty provider list
package demo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProvider_Canonical(t *testing.T) {
	for _, p := range AllProviders() {
		got, err := ParseProvider(string(p))
		require.NoErrorf(t, err, "parse of %q", p)
		assert.Equal(t, p, got)
	}
}

func TestParseProvider_Aliases(t *testing.T) {
	cases := map[string]Provider{
		"":            ProviderMiniMaxi, // default
		"minimax":     ProviderMiniMaxi,
		"MiniMax-M3":  ProviderMiniMaxi,
		"MINIMAXI":    ProviderMiniMaxi,
		"qwen":        ProviderQwenPlus,
		"qwen-plus":   ProviderQwenPlus,
		"qwen-max":    ProviderQwenMax,
		"qwen3.7-max": ProviderQwenMax,
	}
	for in, want := range cases {
		got, err := ParseProvider(in)
		require.NoErrorf(t, err, "parse of %q", in)
		assert.Equalf(t, want, got, "%s → %s expected", in, want)
	}
}

func TestParseProvider_BadInput(t *testing.T) {
	_, err := ParseProvider("gpt-4o")
	assert.Error(t, err, "unknown provider MUST error")
}

func TestProvider_Model_Backend_EnvKey(t *testing.T) {
	cases := map[Provider]struct {
		model, env string
	}{
		ProviderMiniMaxi: {"MiniMax-M3", "MINIMAX_API_KEY"},
		ProviderQwenPlus: {"qwen3.7-plus", "QWEN_API_KEY"},
		ProviderQwenMax:  {"qwen3.7-max", "QWEN_API_KEY"},
	}
	for p, want := range cases {
		assert.Equalf(t, want.model, p.Model(), "%s model", p)
		assert.NotEmptyf(t, p.Backend(), "%s backend", p)
		assert.Equalf(t, want.env, p.EnvKey(), "%s env", p)
	}
}

func TestPricingFor_RealisticRates(t *testing.T) {
	for _, p := range AllProviders() {
		pp := PricingFor(p)
		assert.Greaterf(t, pp.PromptPer1KUSD, 0.0, "%s prompt rate", p)
		assert.Greaterf(t, pp.CompletionPer1KUSD, 0.0, "%s completion rate", p)
		assert.Greaterf(t, pp.CompletionPer1KUSD, pp.PromptPer1KUSD,
			"%s completion should be >= prompt rate", p)
	}
}

func TestRunMatrix_DryRunAllProviders(t *testing.T) {
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Rows, 3)
	for _, r := range result.Rows {
		assert.Equal(t, "ok", r.Status)
		assert.Equal(t, 7, r.TaskCount)
		assert.Greater(t, r.TotalCostUSD, 0.0)
		assert.Equal(t, "dry-run", r.Source)
	}
}

func TestRunMatrix_RejectsEmpty(t *testing.T) {
	_, err := RunMatrix(context.Background(), nil, nil, false)
	assert.Error(t, err)
	_, err = RunMatrix(context.Background(), []Provider{}, nil, false)
	assert.Error(t, err)
}

func TestRunMatrix_DryRunSkipsLiveWithoutKey(t *testing.T) {
	// Should remain dry-run only when useLive=false, regardless of keys.
	result, err := RunMatrix(context.Background(), []Provider{ProviderQwenPlus}, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Rows[0].Status)
	assert.Equal(t, "dry-run", result.Rows[0].Source)
}

func TestRunMatrix_LiveWithoutKeyReturnsSkipped(t *testing.T) {
	// Live requested but no key supplied. Should NOT error; should mark skipped.
	result, err := RunMatrix(context.Background(), []Provider{ProviderQwenPlus}, map[Provider]string{}, true)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "skipped", result.Rows[0].Status)
	assert.Contains(t, result.Rows[0].ErrorMessage, "api key unset")
	// But dry-run pricing is still emitted so the row is populated.
	assert.Equal(t, 7, result.Rows[0].TaskCount)
}

func TestRenderMarkdown_IncludesExpectedSections(t *testing.T) {
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)
	md := RenderMarkdown(result)
	for _, want := range []string{
		"## Real-Models Harness",
		"### Provider Matrix",
		"### Cost Ranking",
		"| Provider | Model | Status | Tasks | Total Cost | Wall-Clock | p99 |",
		"MiniMax-M3",
		"qwen3.7-plus",
		"qwen3.7-max",
		"### minimaxi",
	} {
		assert.Containsf(t, md, want, "markdown should mention %q", want)
	}
}

func TestRenderMarkdown_CostRankingSortedAscending(t *testing.T) {
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)
	md := RenderMarkdown(result)
	// Find rows in "Cost Ranking" section.
	section := md[strings.Index(md, "### Cost Ranking"):]
	lines := []string{}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "1.") || strings.HasPrefix(line, "2.") || strings.HasPrefix(line, "3.") {
			lines = append(lines, line)
		}
	}
	require.Len(t, lines, 3)
	costs := []float64{}
	for _, line := range lines {
		// crude parse: ... — $0.NNNNNNNN [...]
		idx := strings.Index(line, "$")
		if idx < 0 {
			continue
		}
		tail := line[idx+1:]
		end := strings.Index(tail, " ")
		if end < 0 {
			continue
		}
		var f float64
		_ = json.Unmarshal([]byte(tail[:end]), &f)
		costs = append(costs, f)
	}
	require.Len(t, costs, 3)
	for i := 0; i < len(costs)-1; i++ {
		assert.LessOrEqualf(t, costs[i], costs[i+1], "ranking must be ascending (got %v)", costs)
	}
}

func TestAppendNDJSON_WritesValidJSONLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "matrix.ndjson")
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)

	err = AppendNDJSON(path, result)
	require.NoError(t, err)
	err = AppendNDJSON(path, result) // 2nd append
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 2)
	for i, line := range lines {
		var got MatrixResult
		require.NoErrorf(t, json.Unmarshal([]byte(line), &got), "line %d", i)
		assert.Len(t, got.Rows, 3)
	}
}

func TestEnvKeyMap_PopulatesAllProviders(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "test-key-minimax")
	t.Setenv("QWEN_API_KEY", "test-key-qwen")
	m := EnvKeyMap()
	assert.Equal(t, "test-key-minimax", m[ProviderMiniMaxi])
	assert.Equal(t, "test-key-qwen", m[ProviderQwenPlus])
	assert.Equal(t, "test-key-qwen", m[ProviderQwenMax])
}

func TestResolveAPIKeysFor_Precedence(t *testing.T) {
	override := map[Provider]string{ProviderQwenPlus: "override-key"}
	env := map[Provider]string{
		ProviderMiniMaxi: "env-mini",
		ProviderQwenPlus: "env-qwen",
	}
	out := ResolveAPIKeysFor(AllProviders(), override, env)
	assert.Equal(t, "override-key", out[ProviderQwenPlus],
		"override MUST win over env")
	assert.Equal(t, "env-mini", out[ProviderMiniMaxi],
		"minimax should fall through to env")
}

func TestRoundTrip_EmptyKeyErrors(t *testing.T) {
	_, _, _, err := RoundTrip(context.Background(), ProviderMiniMaxi, "", "hi")
	assert.Error(t, err)
}

func TestBackendFromProvider_RoundTrip(t *testing.T) {
	for _, p := range AllProviders() {
		b := BackendFromProvider(p)
		assert.NotEmptyf(t, string(b), "%s backend", p)
	}
}

func TestWriteAll_Succeeds(t *testing.T) {
	var sb strings.Builder
	n, err := WriteAll(&sb, "hello ", []byte("world\n"))
	require.NoError(t, err)
	assert.Equal(t, len("hello ")+len("world\n"), n)
	assert.Equal(t, "hello world\n", sb.String())
}

func TestProviderPricing_ToPilotPricing(t *testing.T) {
	pp := ProviderPricing{PromptPer1KUSD: 0.005, CompletionPer1KUSD: 0.010}
	got := pp.ToPilotPricing()
	assert.Equal(t, 0.005, got.PromptPer1KUSD)
	assert.Equal(t, 0.010, got.CompletionPer1KUSD)
}

func TestMatrixResult_TotalCost_Aggregates(t *testing.T) {
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)
	want := 0.0
	for _, r := range result.Rows {
		want += r.TotalCostUSD
	}
	assert.InDelta(t, want, result.TotalCost, 1e-9)
}

func TestMatrixResult_StartedBeforeCompleted(t *testing.T) {
	result, err := RunMatrix(context.Background(), AllProviders(), nil, false)
	require.NoError(t, err)
	assert.True(t, result.CompletedAt.After(result.StartedAt) ||
		result.CompletedAt.Equal(result.StartedAt),
		"completed_at must be at or after started_at")
	assert.WithinDuration(t, time.Now(), result.StartedAt, 5*time.Second)
}
