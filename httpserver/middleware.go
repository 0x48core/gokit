package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/0x48core/gokit/logger"
	"github.com/0x48core/gokit/metrics"
)

const (
	metricName     = "http_server"
	unmatchedRoute = "unmatched"
	maxTraceIDLen  = 64
)

// TraceIDHeader is the request and response header that carries the trace ID.
var TraceIDHeader = "X-Trace-Id"

type Middleware func(http.Handler) http.Handler

// Chain wraps h with mws, the first one outermost, so Chain(h, a, b) runs a,
// then b, then h.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// TraceID puts the request's trace ID in its context with logger.WithTraceID,
// so slog calls made with that context log it. The ID is read from
// TraceIDHeader, or generated when the header is missing or malformed, and is
// echoed in the response header.
func TraceID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			traceID := r.Header.Get(TraceIDHeader)
			if validTraceID(traceID) {
				ctx = logger.WithTraceID(ctx, traceID)
			} else {
				ctx, traceID = logger.GenerateTraceID(ctx)
			}
			w.Header().Set(TraceIDHeader, traceID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validTraceID accepts up to 64 letters, digits, dashes and underscores, so a
// client cannot put arbitrary text in the logs.
func validTraceID(id string) bool {
	if id == "" || len(id) > maxTraceIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Recover turns a panic in a handler into a 500 response and logs it with its
// stack. http.ErrAbortHandler is re-panicked, so net/http aborts the response
// as the handler intended.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := wrap(w)
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				slog.ErrorContext(r.Context(), "http handler panic",
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path)
				// The status line cannot change once it is sent.
				if rw.status == 0 {
					http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

// AccessLog logs every request when it finishes, at error level for 5xx
// responses and info level otherwise.
func AccessLog() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := wrap(w)
			next.ServeHTTP(rw, r)

			level := slog.LevelInfo
			if rw.statusCode() >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			slog.Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"route", route(r),
				"status", rw.statusCode(),
				"bytes", rw.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent())
		})
	}
}

// Metrics records each request's latency in the http_server histogram, tagged
// with its method, route and status code.
func Metrics(client metrics.Client) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := wrap(w)
			next.ServeHTTP(rw, r)

			client.Duration(metricName, start,
				metrics.Tag{Key: "method", Value: r.Method},
				metrics.Tag{Key: "route", Value: route(r)},
				metrics.Tag{Key: "status", Value: strconv.Itoa(rw.statusCode())})
		})
	}
}

// route returns the http.ServeMux pattern that matched r, such as
// "GET /orders/{id}", so metrics are not tagged with every distinct path. The
// mux sets it on the request it is given, so it is only visible to middleware
// that passes r on unchanged.
func route(r *http.Request) string {
	if r.Pattern == "" {
		return unmatchedRoute
	}
	return r.Pattern
}
