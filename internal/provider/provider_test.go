package provider

import (
	"context"
	"strings"
	"testing"
)

func TestProvider_Names(t *testing.T) {
	cases := []struct {
		p    Provider
		want string
	}{
		{NewMinimax("MiniMax-M3", "sk-test"), "minimax/MiniMax-M3"},
		{NewQwen("qwen3.7-plus", "sk-test"), "qwen/qwen3.7-plus"},
		{NewQwen("qwen3.7-max", "sk-test"), "qwen/qwen3.7-max"},
		{NewDummy("dummy", "test", nil), "dummy/dummy"},
	}
	for _, c := range cases {
		if got := c.p.Name(); got != c.want {
			t.Errorf("Name()=%q want %q", got, c.want)
		}
	}
}

func TestProvider_EstimateCost_Minimax(t *testing.T) {
	p := NewMinimax("MiniMax-M3", "sk-test")
	cost := p.EstimateCost(1000, 1000)
	want := 0.0010 + 0.0020
	if !approxEqual(cost.USD, want) {
		t.Errorf("EstimateCost(1000,1000)=%v want %v", cost.USD, want)
	}
	if cost.Currency != "USD" {
		t.Errorf("currency=%q want USD", cost.Currency)
	}
}

func TestProvider_EstimateCost_QwenMax(t *testing.T) {
	p := NewQwen("qwen3.7-max", "sk-test")
	cost := p.EstimateCost(1000, 1000)
	want := 0.0020 + 0.0060
	if !approxEqual(cost.USD, want) {
		t.Errorf("EstimateCost(1000,1000)=%v want %v", cost.USD, want)
	}
}

func TestProvider_EstimateCost_QwenPlus(t *testing.T) {
	p := NewQwen("qwen3.7-plus", "sk-test")
	cost := p.EstimateCost(1000, 1000)
	want := 0.0004 + 0.0012
	if !approxEqual(cost.USD, want) {
		t.Errorf("EstimateCost(1000,1000)=%v want %v", cost.USD, want)
	}
}

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestProvider_EstimateCost_UnknownModel_Zero(t *testing.T) {
	p := NewDummy("ghost-model", "sk-test", nil)
	cost := p.EstimateCost(1000, 1000)
	if cost.USD != 0 {
		t.Errorf("ghost-model cost=%v want 0", cost.USD)
	}
}

func TestProvider_DummyChat_ResponseMatchesPrompt(t *testing.T) {
	d := NewDummy("dummy", "sk-test", func(model, prompt string) (string, error) {
		return "echo:" + prompt, nil
	})
	resp, err := d.Chat(context.Background(), Request{Model: "dummy", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "echo:hello") {
		t.Errorf("got %q", resp.Text)
	}
}

func TestProvider_Minimax_EndpointAndAuthHeader(t *testing.T) {
	m := NewMinimax("MiniMax-M3", "secret-key").(*httpProvider)
	req, err := m.buildHTTPRequest(context.Background(), Request{Model: "MiniMax-M3", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://api.minimaxi.com/v1/chat/completions" {
		t.Errorf("URL=%s", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret-key" {
		t.Errorf("Authorization=%q", got)
	}
}

func TestProvider_Qwen_Endpoint(t *testing.T) {
	q := NewQwen("qwen3.7-max", "secret-key").(*httpProvider)
	req, err := q.buildHTTPRequest(context.Background(), Request{Model: "qwen3.7-max", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://token-plan.cn-beijing.maas.aliyuncs.com/v1/chat/completions" {
		t.Errorf("URL=%s", req.URL.String())
	}
}
