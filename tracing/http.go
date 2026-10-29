package tracing

import (
	"net"
	"net/http"
	"strings"

	"github.com/0x48core/gokit/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// TraceIDHeader is the response header that carries the trace ID. It matches
// httpserver.TraceIDHeader.
var TraceIDHeader = "X-Trace-Id"

// Middleware starts a server span for every request, continuing the trace in
// its traceparent header, and sets the logger trace ID to the span's trace ID.
// The span is named after the http.ServeMux pattern that matched, such as
// "GET /orders/{id}". Use it in place of httpserver.TraceID: when tracing is
// disabled and the request has no traceparent, it generates a trace ID the
// same way.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			method := normalizeMethod(r.Method)
			ctx, span := otel.Tracer(tracerName).Start(ctx, method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(method),
					semconv.URLPath(r.URL.Path),
					semconv.URLScheme(scheme(r)),
					semconv.ServerAddress(hostname(r.Host)),
					semconv.UserAgentOriginal(r.UserAgent())))
			defer span.End()

			traceID := ""
			if sc := span.SpanContext(); sc.HasTraceID() {
				traceID = sc.TraceID().String()
				ctx = logger.WithTraceID(ctx, traceID)
			} else {
				ctx, traceID = logger.GenerateTraceID(ctx)
			}
			w.Header().Set(TraceIDHeader, traceID)

			sw := &statusWriter{ResponseWriter: w}
			r = r.WithContext(ctx)
			next.ServeHTTP(sw, r)

			// The mux sets the pattern on r, so it is known only now.
			if r.Pattern != "" {
				route := r.Pattern
				if _, path, ok := strings.Cut(route, " "); ok {
					route = path
				}
				span.SetName(method + " " + route)
				span.SetAttributes(semconv.HTTPRoute(route))
			}
			span.SetAttributes(semconv.HTTPResponseStatusCode(sw.statusCode()))
			if sw.statusCode() >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(sw.statusCode()))
			}
		})
	}
}

// Transport returns a RoundTripper that starts a client span for every request
// and sends the trace context in its headers. The span ends when the response
// headers arrive, not when the body is read. base may be nil to use
// http.DefaultTransport.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	method := normalizeMethod(req.Method)
	ctx, span := otel.Tracer(tracerName).Start(req.Context(), method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.HTTPRequestMethodKey.String(method),
			semconv.URLFull(redactedURL(req)),
			semconv.ServerAddress(req.URL.Hostname())))
	defer span.End()

	// A RoundTripper must not modify the request it is given.
	req = req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
	if resp.StatusCode >= http.StatusBadRequest {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}

// redactedURL drops the user info and query, which may hold credentials.
func redactedURL(req *http.Request) string {
	u := *req.URL
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	return u.String()
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodConnect: true, http.MethodOptions: true, http.MethodTrace: true,
}

// normalizeMethod maps unknown methods to "_OTHER", as the semantic
// conventions ask, so clients cannot create unbounded span names.
func normalizeMethod(method string) string {
	if knownMethods[method] {
		return method
	}
	return "_OTHER"
}

// hostname strips the port from a Host header.
func hostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// statusWriter records the status code of a response.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 && status >= http.StatusOK {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush, Hijack and deadlines on the
// underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
