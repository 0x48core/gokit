package httpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0x48core/gokit/logger"
	"github.com/0x48core/gokit/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/suite"
)

type TestClientSuite struct {
	suite.Suite
	calls  atomic.Int32
	server *httptest.Server
	// respond decides the response to the nth call, starting at 1.
	respond func(n int32, w http.ResponseWriter, r *http.Request)
}

func TestTestClientSuite(t *testing.T) {
	suite.Run(t, new(TestClientSuite))
}

func (suite *TestClientSuite) SetupTest() {
	suite.calls.Store(0)
	suite.respond = func(int32, http.ResponseWriter, *http.Request) {}
	suite.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suite.respond(suite.calls.Add(1), w, r)
	}))
}

func (suite *TestClientSuite) TearDownTest() {
	suite.server.Close()
}

func fastRetries(n int) *Config {
	return &Config{MaxRetries: n, RetryWaitMin: time.Millisecond, RetryWaitMax: 5 * time.Millisecond}
}

func failFirst(failures int32, status int) func(int32, http.ResponseWriter, *http.Request) {
	return func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n <= failures {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("try again"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}

func (suite *TestClientSuite) get(ctx context.Context, client *http.Client) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, suite.server.URL, nil)
	suite.Require().NoError(err)
	return client.Do(req)
}

func (suite *TestClientSuite) Test_New_Defaults() {
	// Action
	client := New(nil, nil)

	// Assert
	suite.Equal(defaultTimeout, client.Timeout)
	it := client.Transport.(*instrumentTransport)
	tr := it.next.(*http.Transport)
	suite.Equal(defaultMaxIdleConnsPerHost, tr.MaxIdleConnsPerHost)
	suite.Equal(defaultIdleConnTimeout, tr.IdleConnTimeout)
}

func (suite *TestClientSuite) Test_NoRetriesByDefault() {
	// Arrange
	suite.respond = failFirst(1, http.StatusServiceUnavailable)

	// Action
	resp, err := suite.get(context.Background(), New(nil, nil))

	// Assert
	suite.Require().NoError(err)
	resp.Body.Close()
	suite.Equal(http.StatusServiceUnavailable, resp.StatusCode)
	suite.Equal(int32(1), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Retry_RetryableStatus() {
	// Arrange
	suite.respond = failFirst(2, http.StatusBadGateway)

	// Action
	resp, err := suite.get(context.Background(), New(fastRetries(3), nil))

	// Assert
	suite.Require().NoError(err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	suite.Equal("ok", string(body))
	suite.Equal(int32(3), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Retry_GivesUp() {
	// Arrange
	suite.respond = failFirst(10, http.StatusServiceUnavailable)

	// Action
	resp, err := suite.get(context.Background(), New(fastRetries(2), nil))

	// Assert
	suite.Require().NoError(err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	suite.Equal(http.StatusServiceUnavailable, resp.StatusCode)
	suite.Equal("try again", string(body), "the last response is returned unread")
	suite.Equal(int32(3), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Retry_NotRetryableStatus() {
	// Arrange
	suite.respond = failFirst(1, http.StatusInternalServerError)

	// Action
	resp, err := suite.get(context.Background(), New(fastRetries(3), nil))

	// Assert
	suite.Require().NoError(err)
	resp.Body.Close()
	suite.Equal(http.StatusInternalServerError, resp.StatusCode)
	suite.Equal(int32(1), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Retry_PostNeedsIdempotencyKey() {
	// Arrange
	var bodies []string
	suite.respond = func(n int32, w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		failFirst(1, http.StatusServiceUnavailable)(n, w, r)
	}
	client := New(fastRetries(3), nil)

	// Action
	resp, err := client.Post(suite.server.URL, "text/plain", strings.NewReader("order"))
	suite.Require().NoError(err)
	resp.Body.Close()
	plainCalls := suite.calls.Load()

	req, err := http.NewRequest(http.MethodPost, suite.server.URL, strings.NewReader("order"))
	suite.Require().NoError(err)
	req.Header.Set(IdempotencyKeyHeader, "order-42")
	suite.calls.Store(0)
	bodies = nil
	resp, err = client.Do(req)
	suite.Require().NoError(err)
	resp.Body.Close()

	// Assert
	suite.Equal(int32(1), plainCalls)
	suite.Equal(int32(2), suite.calls.Load())
	suite.Equal([]string{"order", "order"}, bodies, "the body is replayed")
}

func (suite *TestClientSuite) Test_Retry_UnreplayableBody() {
	// Arrange
	suite.respond = failFirst(1, http.StatusServiceUnavailable)
	req, err := http.NewRequest(http.MethodPut, suite.server.URL, io.NopCloser(strings.NewReader("x")))
	suite.Require().NoError(err)

	// Action
	resp, err := New(fastRetries(3), nil).Do(req)

	// Assert
	suite.Require().NoError(err)
	resp.Body.Close()
	suite.Equal(int32(1), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Retry_TransportError() {
	// Arrange
	var attempts atomic.Int32
	failing := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, io.ErrUnexpectedEOF
	})

	// Action
	_, err := suite.get(context.Background(), New(fastRetries(2), nil, WithTransport(failing)))

	// Assert
	suite.ErrorIs(err, io.ErrUnexpectedEOF)
	suite.Equal(int32(3), attempts.Load())
}

func (suite *TestClientSuite) Test_Retry_StopsOnContextCancel() {
	// Arrange
	suite.respond = failFirst(10, http.StatusServiceUnavailable)
	conf := &Config{MaxRetries: 5, RetryWaitMin: time.Second, RetryWaitMax: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Action
	start := time.Now()
	_, err := suite.get(ctx, New(conf, nil))

	// Assert
	suite.ErrorIs(err, context.DeadlineExceeded)
	suite.Less(time.Since(start), 500*time.Millisecond)
	suite.Equal(int32(1), suite.calls.Load())
}

func (suite *TestClientSuite) Test_Backoff() {
	// Arrange
	t := &retryTransport{waitMin: 100 * time.Millisecond, waitMax: time.Second}
	retryAfter := &http.Response{Header: http.Header{"Retry-After": []string{"3"}}}

	// Action & Assert
	suite.Equal(100*time.Millisecond, t.backoff(0, nil))
	for range 100 {
		wait := t.backoff(2, nil)
		suite.GreaterOrEqual(wait, 100*time.Millisecond)
		suite.LessOrEqual(wait, 400*time.Millisecond)
		suite.LessOrEqual(t.backoff(60, nil), time.Second)
	}
	suite.Equal(time.Second, t.backoff(0, retryAfter), "Retry-After is capped at waitMax")
}

func (suite *TestClientSuite) Test_TraceIDHeader() {
	// Arrange
	var got string
	suite.respond = func(_ int32, _ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(TraceIDHeader)
	}
	ctx := logger.WithTraceID(context.Background(), "abc123")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, suite.server.URL, nil)
	suite.Require().NoError(err)

	// Action
	resp, err := New(nil, nil).Do(req)

	// Assert
	suite.Require().NoError(err)
	resp.Body.Close()
	suite.Equal("abc123", got)
	suite.Empty(req.Header.Get(TraceIDHeader), "the caller's request must not be modified")
}

func (suite *TestClientSuite) Test_Middleware_RunsPerAttempt() {
	// Arrange
	suite.respond = failFirst(1, http.StatusServiceUnavailable)
	var order []string
	mw := func(name string) Middleware {
		return func(next http.RoundTripper) http.RoundTripper {
			return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				order = append(order, name)
				return next.RoundTrip(r)
			})
		}
	}

	// Action
	resp, err := suite.get(context.Background(), New(fastRetries(1), nil, WithMiddleware(mw("a"), mw("b"))))

	// Assert
	suite.Require().NoError(err)
	resp.Body.Close()
	suite.Equal([]string{"a", "b", "a", "b"}, order)
}

func (suite *TestClientSuite) Test_Metrics() {
	// Arrange
	suite.respond = failFirst(1, http.StatusServiceUnavailable)
	reg := prometheus.NewRegistry()
	client := New(fastRetries(1), metrics.NewClientWithRegisterer("test", "test", reg))

	// Action
	resp, err := suite.get(context.Background(), client)
	suite.Require().NoError(err)
	resp.Body.Close()

	// Assert
	families, err := reg.Gather()
	suite.Require().NoError(err)
	suite.Require().Len(families, 1)
	suite.Equal("test_http_client", families[0].GetName())
	suite.Require().Len(families[0].GetMetric(), 1, "one sample per call, not per attempt")
	labels := map[string]string{}
	for _, l := range families[0].GetMetric()[0].GetLabel() {
		labels[l.GetName()] = l.GetValue()
	}
	suite.Equal("200", labels["status"])
	suite.Equal(strings.TrimPrefix(suite.server.URL, "http://"), labels["host"])
	suite.Equal(uint64(1), families[0].GetMetric()[0].GetHistogram().GetSampleCount())
}

func (suite *TestClientSuite) Test_Metrics_Error() {
	// Arrange
	reg := prometheus.NewRegistry()
	failing := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})
	client := New(nil, metrics.NewClientWithRegisterer("test", "test", reg), WithTransport(failing))

	// Action
	_, err := suite.get(context.Background(), client)

	// Assert
	suite.Require().Error(err)
	families, err := reg.Gather()
	suite.Require().NoError(err)
	suite.Require().Len(families, 1)
	for _, l := range families[0].GetMetric()[0].GetLabel() {
		if l.GetName() == "status" {
			suite.Equal("error", l.GetValue())
		}
	}
}

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
