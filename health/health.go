package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultTimeout = 2 * time.Second

	StatusOK   = "ok"
	StatusFail = "fail"
)

// Check reports whether a dependency is usable. It should return when ctx is
// done. Methods such as redis Client.Ping and sql.DB.PingContext fit as-is.
type Check func(ctx context.Context) error

type CheckResult struct {
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type Result struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

type namedCheck struct {
	name  string
	check Check
}

type Checker struct {
	mu       sync.RWMutex
	checks   []namedCheck
	timeout  time.Duration
	draining atomic.Bool

	// lastStatus holds each check's previous status, so failures are logged
	// when they start and end rather than on every probe.
	lastMu     sync.Mutex
	lastStatus map[string]string
}

type Option func(*Checker)

// WithCheck adds a check that readiness runs.
func WithCheck(name string, check Check) Option {
	return func(c *Checker) {
		c.checks = append(c.checks, namedCheck{name: name, check: check})
	}
}

// WithTimeout sets how long each check may take before it fails. The default
// is 2 seconds.
func WithTimeout(d time.Duration) Option {
	return func(c *Checker) {
		c.timeout = d
	}
}

func New(opts ...Option) *Checker {
	c := &Checker{timeout: defaultTimeout, lastStatus: map[string]string{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Register adds a check after New, for dependencies created later.
func (c *Checker) Register(name string, check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = append(c.checks, namedCheck{name: name, check: check})
}

// Drain makes readiness fail from now on without running the checks, so load
// balancers stop sending traffic before the server shuts down.
func (c *Checker) Drain() {
	c.draining.Store(true)
}

// Check runs every check concurrently, each with its own timeout, and reports
// StatusOK only if all of them pass. A panicking check fails.
func (c *Checker) Check(ctx context.Context) Result {
	c.mu.RLock()
	checks := c.checks
	c.mu.RUnlock()

	results := make([]CheckResult, len(checks))
	var wg sync.WaitGroup
	for i, nc := range checks {
		wg.Go(func() {
			results[i] = c.run(ctx, nc)
		})
	}
	wg.Wait()

	res := Result{Status: StatusOK, Checks: make(map[string]CheckResult, len(checks))}
	for i, nc := range checks {
		res.Checks[nc.name] = results[i]
		if results[i].Status != StatusOK {
			res.Status = StatusFail
		}
		c.logTransition(ctx, nc.name, results[i])
	}
	return res
}

func (c *Checker) run(ctx context.Context, nc namedCheck) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- fmt.Errorf("panic: %v", rec)
			}
		}()
		done <- nc.check(ctx)
	}()

	// A check that ignores ctx keeps running in the background, but the
	// probe does not wait for it.
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}

	res := CheckResult{Status: StatusOK, DurationMs: time.Since(start).Milliseconds()}
	if err != nil {
		res.Status = StatusFail
		res.Error = err.Error()
	}
	return res
}

func (c *Checker) logTransition(ctx context.Context, name string, res CheckResult) {
	c.lastMu.Lock()
	prev, seen := c.lastStatus[name]
	c.lastStatus[name] = res.Status
	c.lastMu.Unlock()

	switch {
	case res.Status == StatusFail && prev != StatusFail:
		slog.WarnContext(ctx, "health check failing", "check", name, "error", res.Error)
	case res.Status == StatusOK && seen && prev != StatusOK:
		slog.InfoContext(ctx, "health check recovered", "check", name)
	}
}

// Liveness responds 200 while the process can serve HTTP. It runs no checks,
// so a broken dependency does not get the process restarted.
func (c *Checker) Liveness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, Result{Status: StatusOK})
	})
}

// Readiness runs the checks and responds 200 if all pass, or 503 with each
// check's result if any fails or the checker is draining.
func (c *Checker) Readiness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.draining.Load() {
			writeJSON(w, http.StatusServiceUnavailable, Result{Status: StatusFail})
			return
		}
		res := c.Check(r.Context())
		status := http.StatusOK
		if res.Status != StatusOK {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, res)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
