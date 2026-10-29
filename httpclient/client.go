package httpclient

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/0x48core/gokit/logger"
	"github.com/0x48core/gokit/metrics"
)

const metricName = "http_client"

// TraceIDHeader is the request header that carries the logger trace ID. It
// matches httpserver.TraceIDHeader.
var TraceIDHeader = "X-Trace-Id"

// Middleware wraps a RoundTripper. tracing.Transport is one.
type Middleware func(http.RoundTripper) http.RoundTripper

type options struct {
	base        http.RoundTripper
	middlewares []Middleware
}

type Option func(*options)

// WithTransport replaces the *http.Transport built from Config, for example
// to set TLS options. The pool settings in Config then do not apply.
func WithTransport(base http.RoundTripper) Option {
	return func(o *options) {
		o.base = base
	}
}

// WithMiddleware wraps every attempt, inside the retries, the first one
// outermost. Pass tracing.Transport to get a client span per attempt.
func WithMiddleware(mws ...Middleware) Option {
	return func(o *options) {
		o.middlewares = append(o.middlewares, mws...)
	}
}

// New creates a client. conf may be nil to use the defaults, and metricClient
// may be nil to record no metrics. Each call sends the logger trace ID from
// the request's context in TraceIDHeader, retries as Config allows, and
// records its latency in the http_client histogram.
func New(conf *Config, metricClient metrics.Client, opts ...Option) *http.Client {
	c := Config{}
	if conf != nil {
		c = *conf
	}
	c = c.withDefaults()

	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	var rt http.RoundTripper = o.base
	if rt == nil {
		rt = newTransport(c)
	}
	for i := len(o.middlewares) - 1; i >= 0; i-- {
		rt = o.middlewares[i](rt)
	}
	if c.MaxRetries > 0 {
		rt = &retryTransport{
			next:       rt,
			maxRetries: c.MaxRetries,
			waitMin:    c.RetryWaitMin,
			waitMax:    c.RetryWaitMax,
		}
	}

	return &http.Client{
		Timeout:   c.Timeout,
		Transport: &instrumentTransport{next: rt, metricClient: metricClient},
	}
}

func newTransport(c Config) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   c.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   c.TLSHandshakeTimeout,
		IdleConnTimeout:       c.IdleConnTimeout,
		MaxIdleConns:          c.MaxIdleConnsPerHost * 4,
		MaxIdleConnsPerHost:   c.MaxIdleConnsPerHost,
		MaxConnsPerHost:       c.MaxConnsPerHost,
		ExpectContinueTimeout: time.Second,
	}
}

// instrumentTransport sends the trace ID and records each call's latency,
// retries included.
type instrumentTransport struct {
	next         http.RoundTripper
	metricClient metrics.Client
}

func (t *instrumentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if traceID := logger.GetTraceID(req.Context()); traceID != "" && req.Header.Get(TraceIDHeader) == "" {
		// A RoundTripper must not modify the request it is given.
		req = req.Clone(req.Context())
		req.Header.Set(TraceIDHeader, traceID)
	}

	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	if t.metricClient != nil {
		status := "error"
		if err == nil {
			status = strconv.Itoa(resp.StatusCode)
		}
		t.metricClient.Duration(metricName, start,
			metrics.Tag{Key: "method", Value: req.Method},
			metrics.Tag{Key: "host", Value: req.URL.Host},
			metrics.Tag{Key: "status", Value: status})
	}
	return resp, err
}
