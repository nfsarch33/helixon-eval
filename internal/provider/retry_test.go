package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryPolicy_DelayGrowsExponentially(t *testing.T) {
	p := DefaultRetryPolicy()
	d0 := p.Delay(0)
	d1 := p.Delay(1)
	d2 := p.Delay(2)
	if d0 <= 0 || d1 <= 0 || d2 <= 0 {
		t.Fatalf("delays must be positive: %v %v %v", d0, d1, d2)
	}
	if d1 < d0 {
		t.Errorf("delay(1)=%v < delay(0)=%v", d1, d0)
	}
	if d2 < d1 {
		t.Errorf("delay(2)=%v < delay(1)=%v", d2, d1)
	}
}

func TestRetryPolicy_CapAt8s(t *testing.T) {
	p := DefaultRetryPolicy()
	if d := p.Delay(20); d > 9*time.Second {
		t.Errorf("cap exceeded: %v", d)
	}
}

func TestRetryPolicy_AttemptsMax3(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.MaxAttempts != 3 {
		t.Errorf("MaxAttempts=%d want 3", p.MaxAttempts)
	}
}

func TestRetryPolicy_ShouldRetryOnTransient(t *testing.T) {
	p := DefaultRetryPolicy()
	if !p.ShouldRetry(errors.New("HTTP 429")) {
		t.Errorf("should retry on 429")
	}
	if !p.ShouldRetry(errors.New("HTTP 503")) {
		t.Errorf("should retry on 503")
	}
	if p.ShouldRetry(errors.New("HTTP 401")) {
		t.Errorf("should not retry on 401")
	}
}

func TestCallWithRetry_SucceedsOnSecondAttempt(t *testing.T) {
	calls := 0
	p := DefaultRetryPolicy()
	resp, attempts, err := CallWithRetry(context.Background(), p, func(ctx context.Context) (Response, error) {
		calls++
		if calls == 1 {
			return Response{}, errors.New("HTTP 503 service unavailable")
		}
		return Response{Text: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" {
		t.Errorf("text=%q", resp.Text)
	}
	if attempts != 2 {
		t.Errorf("attempts=%d want 2", attempts)
	}
}

func TestCallWithRetry_ExhaustsAndReturnsLastError(t *testing.T) {
	p := DefaultRetryPolicy()
	_, attempts, err := CallWithRetry(context.Background(), p, func(ctx context.Context) (Response, error) {
		return Response{}, errors.New("HTTP 500 always")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != p.MaxAttempts {
		t.Errorf("attempts=%d want %d", attempts, p.MaxAttempts)
	}
}
