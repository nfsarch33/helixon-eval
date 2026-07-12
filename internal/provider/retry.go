package provider

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"time"
)

// RetryPolicy configures exponential backoff with jitter.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	JitterMin   time.Duration
	JitterMax   time.Duration
}

// DefaultRetryPolicy is the R6 spec default: max 3 attempts, base 100ms,
// max 8s, jitter 50-200ms.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    8 * time.Second,
		JitterMin:   50 * time.Millisecond,
		JitterMax:   200 * time.Millisecond,
	}
}

// Delay returns the wait duration before the given attempt (0-indexed).
// Formula: min(MaxDelay, BaseDelay * 2^attempt) + uniform jitter.
func (p RetryPolicy) Delay(attempt int) time.Duration {
	exp := math.Pow(2, float64(attempt))
	base := time.Duration(float64(p.BaseDelay) * exp)
	if base > p.MaxDelay {
		base = p.MaxDelay
	}
	jitter := p.JitterMin
	if span := p.JitterMax - p.JitterMin; span > 0 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		n := binary.BigEndian.Uint64(b[:])
		jitter = p.JitterMin + time.Duration(n%uint64(span))
	}
	return base + jitter
}

// ShouldRetry returns true for transient HTTP errors (429 / 5xx) and
// network/timeout errors; false for 4xx client errors.
func (p RetryPolicy) ShouldRetry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "429") || strings.Contains(msg, "500") || strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout") {
		return true
	}
	return false
}

// ChatFunc is the single-attempt callable passed to CallWithRetry.
type ChatFunc func(ctx context.Context) (Response, error)

// CallWithRetry calls fn with exponential-backoff retries according to
// policy. Returns the final response, the number of attempts made,
// and the final error (nil on success).
func CallWithRetry(ctx context.Context, p RetryPolicy, fn ChatFunc) (Response, int, error) {
	var lastErr error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		resp, err := fn(ctx)
		if err == nil {
			return resp, attempt + 1, nil
		}
		lastErr = err
		if !p.ShouldRetry(err) || attempt == p.MaxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return Response{}, attempt + 1, ctx.Err()
		case <-time.After(p.Delay(attempt)):
		}
	}
	return Response{}, p.MaxAttempts, lastErr
}
