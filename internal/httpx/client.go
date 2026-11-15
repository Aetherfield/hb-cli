// Package httpx is the shared HTTP client: sane timeouts, jittered retries
// for idempotent requests, cooldown-aware 429 handling, and header redaction.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const UserAgent = "hb-cli/0.1 (+red-packet helper)"

// RateLimitedError is returned on HTTP 429. The client never retries it
// automatically: callers schedule the next attempt using RetryAfter.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
}

type Client struct {
	HTTP       *http.Client
	MaxRetries int           // retries for idempotent methods on network error / 5xx
	BaseDelay  time.Duration // first backoff step
}

func New() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
			// Never silently follow redirects: callers decide.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		MaxRetries: 2,
		BaseDelay:  400 * time.Millisecond,
	}
}

func idempotent(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// Do sends req. Idempotent requests are retried with jittered exponential
// backoff on transport errors and 5xx. A 429 yields *RateLimitedError.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	attempts := 1
	if idempotent(req.Method) {
		attempts += c.MaxRetries
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			d := c.BaseDelay << (i - 1)
			d += time.Duration(rand.Int64N(int64(d/2) + 1))
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			ra := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			return nil, &RateLimitedError{RetryAfter: ra}
		}
		if resp.StatusCode >= 500 && i < attempts-1 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return nil, lastErr
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return time.Second
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return time.Second
}

var sensitiveHeaders = map[string]bool{
	"cookie": true, "set-cookie": true, "authorization": true,
	"x-csrf-token": true, "proxy-authorization": true,
}

// RedactHeaders returns a loggable copy of h with secrets masked.
func RedactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = "[redacted]"
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}
