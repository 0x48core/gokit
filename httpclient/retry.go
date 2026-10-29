package httpclient

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// IdempotencyKeyHeader marks a request as safe to retry whatever its method.
const IdempotencyKeyHeader = "Idempotency-Key"

// maxDrain is how much of a failed response body is read so its connection
// can be reused.
const maxDrain = 4 << 10

var retryableStatus = map[int]bool{
	http.StatusTooManyRequests:    true,
	http.StatusBadGateway:         true,
	http.StatusServiceUnavailable: true,
	http.StatusGatewayTimeout:     true,
}

var idempotentMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
	http.MethodTrace: true, http.MethodPut: true, http.MethodDelete: true,
}

type retryTransport struct {
	next       http.RoundTripper
	maxRetries int
	waitMin    time.Duration
	waitMax    time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !retryable(req) {
		return t.next.RoundTrip(req)
	}

	for attempt := 0; ; attempt++ {
		attemptReq, err := rewind(req, attempt)
		if err != nil {
			return nil, err
		}

		resp, err := t.next.RoundTrip(attemptReq)
		if attempt == t.maxRetries || !shouldRetry(req.Context(), resp, err) {
			return resp, err
		}

		wait := t.backoff(attempt, resp)
		if resp != nil {
			_, _ = io.CopyN(io.Discard, resp.Body, maxDrain)
			_ = resp.Body.Close()
		}

		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

// retryable reports whether req may be sent again: its method is idempotent
// or it has an Idempotency-Key, and its body, if any, can be replayed.
func retryable(req *http.Request) bool {
	if !idempotentMethods[req.Method] && req.Header.Get(IdempotencyKeyHeader) == "" {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// rewind returns req for the first attempt, and a copy with a fresh body for
// later ones.
func rewind(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 {
		return req, nil
	}
	r := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	return r, nil
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return retryableStatus[resp.StatusCode]
}

// backoff waits a random time up to waitMin doubled per attempt, capped at
// waitMax, or what Retry-After asks for, also capped at waitMax.
func (t *retryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs >= 0 {
			return min(time.Duration(secs)*time.Second, t.waitMax)
		}
	}
	ceiling := t.waitMax
	if attempt < 30 {
		ceiling = min(t.waitMin<<attempt, t.waitMax)
	}
	return t.waitMin + rand.N(ceiling-t.waitMin+1) //nolint:gosec // jitter, not security
}
