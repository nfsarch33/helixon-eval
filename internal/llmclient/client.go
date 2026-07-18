// Package llmclient provides an OpenAI-compatible HTTP client for the three
// production LLM backends used by helixon-eval:
//   - MiniMaxi (M3, abab)         -> https://api.minimaxi.com/v1
//   - Aliyun Qwen (qwen3.7-plus) -> https://cn-beijing.maas.aliyuncs.com/compatible-mode/v1
//   - Aliyun Qwen (qwen3.7-max)  -> https://cn-beijing.maas.aliyuncs.com/compatible-mode/v1
//
// All three expose an OpenAI-compatible /v1/chat/completions surface, so
// a single client implementation handles all three. Per v18681-1.
package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Backend identifies one of the three supported LLM providers.
type Backend string

const (
	BackendMiniMaxi Backend = "minimax"
	BackendQwenPlus Backend = "qwen-plus"
	BackendQwenMax  Backend = "qwen-max"
)

// String returns the canonical identifier for the backend.
func (b Backend) String() string { return string(b) }

// Endpoint returns the base URL for the backend's OpenAI-compatible API.
func (b Backend) Endpoint() string {
	switch b {
	case BackendMiniMaxi:
		return "https://api.minimaxi.com/v1"
	case BackendQwenPlus, BackendQwenMax:
		return "https://cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	default:
		return ""
	}
}

// DefaultModel returns the canonical model id for the backend.
func (b Backend) DefaultModel() string {
	switch b {
	case BackendMiniMaxi:
		return "MiniMax-M3"
	case BackendQwenPlus:
		return "qwen3.7-plus"
	case BackendQwenMax:
		return "qwen3.7-max"
	default:
		return ""
	}
}

// AllBackends returns the canonical list of supported backends.
func AllBackends() []Backend {
	return []Backend{BackendMiniMaxi, BackendQwenPlus, BackendQwenMax}
}

// ChatRequest mirrors the OpenAI /v1/chat/completions payload.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

// Message is a single OpenAI chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the subset of the OpenAI response we consume.
type ChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Client is a thin OpenAI-compatible HTTP client.
type Client struct {
	backend  Backend
	apiKey   string
	http     *http.Client
	upstream string // empty = use backend.Endpoint()
}

// New returns a Client for the given backend and API key. Empty apiKey is
// rejected; call sites must resolve the key via 1Password at startup.
func New(backend Backend, apiKey string) (*Client, error) {
	return newClient(backend, apiKey, &http.Client{Timeout: 120 * time.Second})
}

// NewWithUpstream returns a Client whose Chat() dials the supplied
// upstream URL instead of the canonical backend endpoint. Intended for
// tests using httptest.NewServer; production code MUST use New().
func NewWithUpstream(backend Backend, apiKey, upstream string) (*Client, error) {
	return newClientWithUpstream(backend, apiKey, &http.Client{Timeout: 120 * time.Second}, upstream)
}

// newClient is the internal constructor that allows tests to inject a
// custom http.Client (for httptest targeting). The production code path
// always uses New().
func newClient(backend Backend, apiKey string, hc *http.Client) (*Client, error) {
	if backend.Endpoint() == "" {
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("apiKey is required (resolve via 1Password)")
	}
	return &Client{
		backend: backend,
		apiKey:  apiKey,
		http:    hc,
	}, nil
}

// newClientWithUpstream is the test-only constructor that lets the
// caller override the dial target. Empty upstream falls back to
// backend.Endpoint().
func newClientWithUpstream(backend Backend, apiKey string, hc *http.Client, upstream string) (*Client, error) {
	c, err := newClient(backend, apiKey, hc)
	if err != nil {
		return nil, err
	}
	c.upstream = upstream
	return c, nil
}

// Backend returns the configured backend identifier.
func (c *Client) Backend() Backend { return c.backend }

// Chat sends a request and returns the response. Network/timeout errors
// are returned wrapped; HTTP 4xx/5xx responses surface as ResponseError
// with the status code and body.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.upstream != "" {
		return c.chatAt(ctx, c.upstream, req)
	}
	return c.chatAt(ctx, c.backend.Endpoint(), req)
}

// chatAt is the testable seam: production code calls Chat which routes
// through chatAt with the canonical endpoint; tests call chatAt with a
// httptest URL.
func (c *Client) chatAt(ctx context.Context, baseURL string, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.backend.DefaultModel()
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &ResponseError{
			Status:  resp.StatusCode,
			Body:    string(respBody),
			Backend: c.backend,
		}
	}
	var out ChatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("unmarshal response (body=%s): %w", string(respBody[:min(len(respBody), 200)]), err)
	}
	return &out, nil
}

// ResponseError surfaces a non-2xx HTTP response.
type ResponseError struct {
	Status  int
	Body    string
	Backend Backend
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s chat: HTTP %d: %s", e.Backend, e.Status, e.Body)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
