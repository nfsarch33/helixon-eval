// Package provider defines the LLM provider abstraction used by
// helixon-eval R6 to score MiniMax-M3, qwen3.7-plus, and qwen3.7-max
// backends. API keys are piped in via stdin (Pattern A from
// no-shell-leak.mdc) and never appear on argv or in env vars.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Request is the minimal chat request shape shared by all providers.
type Request struct {
	Model  string `json:"model"`
	Prompt string `json:"-"`
}

// Cell is a (provider, model) pair used by the R6 leaderboard.
type Cell struct {
	Provider string
	Model    string
}

// String returns "provider/model".
func (c Cell) String() string {
	return c.Provider + "/" + c.Model
}

// Response is the minimal chat response shape.
type Response struct {
	Text      string
	InTokens  int
	OutTokens int
	Raw       []byte
}

// Cost is the USD-equivalent cost for a single call.
type Cost struct {
	USD      float64
	Currency string
}

// Provider is implemented by every backend.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request) (Response, error)
	EstimateCost(inTokens, outTokens int) Cost
}

// --- Dummy provider (used for dry-runs) ---

// DummyHandler produces a response text for a given prompt.
type DummyHandler func(model, prompt string) (string, error)

type dummyProvider struct {
	model   string
	apiKey  string
	handler DummyHandler
}

// NewDummy creates a non-network provider used for harness smoke tests.
func NewDummy(model, apiKey string, h DummyHandler) Provider {
	if h == nil {
		h = func(model, prompt string) (string, error) {
			return "dummy:" + prompt, nil
		}
	}
	return &dummyProvider{model: model, apiKey: apiKey, handler: h}
}

func (d *dummyProvider) Name() string { return "dummy/" + d.model }

func (d *dummyProvider) Chat(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	text, err := d.handler(req.Model, req.Prompt)
	if err != nil {
		return Response{}, err
	}
	return Response{Text: text, InTokens: len(req.Prompt), OutTokens: len(text)}, nil
}

func (d *dummyProvider) EstimateCost(inTokens, outTokens int) Cost {
	if rate, ok := pricingTable[d.model]; ok {
		return Cost{
			USD:      float64(inTokens)/1000.0*rate.InPerK + float64(outTokens)/1000.0*rate.OutPerK,
			Currency: "USD",
		}
	}
	return Cost{USD: 0, Currency: "USD"}
}

// --- MiniMax provider ---

const (
	minimaxEndpoint = "https://api.minimaxi.com/v1/chat/completions"
	qwenEndpoint    = "https://token-plan.cn-beijing.maas.aliyuncs.com/v1/chat/completions"
)

type httpProvider struct {
	providerName string
	model        string
	apiKey       string
	endpoint     string
}

func newHTTPProvider(providerName, model, apiKey, endpoint string) *httpProvider {
	return &httpProvider{
		providerName: providerName,
		model:        model,
		apiKey:       apiKey,
		endpoint:     endpoint,
	}
}

// NewMinimax creates a MiniMax-M3 (or other minimax model) provider.
func NewMinimax(model, apiKey string) Provider {
	return newHTTPProvider("minimax", model, apiKey, minimaxEndpoint)
}

// NewQwen creates a qwen3.7-plus or qwen3.7-max provider.
func NewQwen(model, apiKey string) Provider {
	return newHTTPProvider("qwen", model, apiKey, qwenEndpoint)
}

func (p *httpProvider) Name() string { return p.providerName + "/" + p.model }

func (p *httpProvider) Chat(ctx context.Context, req Request) (Response, error) {
	httpReq, err := p.buildHTTPRequest(ctx, req)
	if err != nil {
		return Response{}, err
	}
	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, err
	}
	if httpResp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("%s status %d: %s", p.Name(), httpResp.StatusCode, truncate(string(body), 200))
	}
	text := extractText(body)
	return Response{Text: text, InTokens: len(req.Prompt), OutTokens: len(text), Raw: body}, nil
}

func (p *httpProvider) buildHTTPRequest(ctx context.Context, req Request) (*http.Request, error) {
	payload := map[string]any{
		"model": req.Model,
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	return httpReq, nil
}

func (p *httpProvider) EstimateCost(inTokens, outTokens int) Cost {
	if rate, ok := pricingTable[p.model]; ok {
		return Cost{
			USD:      float64(inTokens)/1000.0*rate.InPerK + float64(outTokens)/1000.0*rate.OutPerK,
			Currency: "USD",
		}
	}
	return Cost{USD: 0, Currency: "USD"}
}

func extractText(body []byte) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return string(body)
	}
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
