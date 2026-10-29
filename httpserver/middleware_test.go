package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0x48core/gokit/logger"
	"github.com/0x48core/gokit/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/suite"
)

type TestMiddlewareSuite struct {
	suite.Suite
	logs       *bytes.Buffer
	oldDefault *slog.Logger
}

func TestTestMiddlewareSuite(t *testing.T) {
	suite.Run(t, new(TestMiddlewareSuite))
}

func (suite *TestMiddlewareSuite) SetupTest() {
	suite.logs = &bytes.Buffer{}
	suite.oldDefault = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(suite.logs, nil)))
}

func (suite *TestMiddlewareSuite) TearDownTest() {
	slog.SetDefault(suite.oldDefault)
}

func (suite *TestMiddlewareSuite) logLines() []map[string]any {
	var lines []map[string]any
	for line := range strings.Lines(suite.logs.String()) {
		m := map[string]any{}
		suite.Require().NoError(json.Unmarshal([]byte(line), &m))
		lines = append(lines, m)
	}
	return lines
}

func (suite *TestMiddlewareSuite) Test_Chain_Order() {
	// Arrange
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), mw("a"), mw("b"))

	// Action
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	// Assert
	suite.Equal([]string{"a", "b", "handler"}, order)
}

func (suite *TestMiddlewareSuite) Test_TraceID_FromHeader() {
	// Arrange
	var got string
	h := TraceID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = logger.GetTraceID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(TraceIDHeader, "abc-123")
	rec := httptest.NewRecorder()

	// Action
	h.ServeHTTP(rec, req)

	// Assert
	suite.Equal("abc-123", got)
	suite.Equal("abc-123", rec.Header().Get(TraceIDHeader))
}

func (suite *TestMiddlewareSuite) Test_TraceID_Generated() {
	for name, header := range map[string]string{
		"missing":   "",
		"malformed": "bad id\nwith newline",
		"too long":  strings.Repeat("a", maxTraceIDLen+1),
	} {
		suite.Run(name, func() {
			// Arrange
			var got string
			h := TraceID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = logger.GetTraceID(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(TraceIDHeader, header)
			rec := httptest.NewRecorder()

			// Action
			h.ServeHTTP(rec, req)

			// Assert
			suite.Len(got, 32)
			suite.NotEqual(header, got)
			suite.Equal(got, rec.Header().Get(TraceIDHeader))
		})
	}
}

func (suite *TestMiddlewareSuite) Test_Recover_Panic() {
	// Arrange
	h := Recover()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()

	// Action
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))

	// Assert
	suite.Equal(http.StatusInternalServerError, rec.Code)
	lines := suite.logLines()
	suite.Require().Len(lines, 1)
	suite.Equal("http handler panic", lines[0]["msg"])
	suite.Equal("boom", lines[0]["panic"])
	suite.Contains(lines[0]["stack"], "middleware_test.go")
}

func (suite *TestMiddlewareSuite) Test_Recover_PanicAfterWrite() {
	// Arrange
	h := Recover()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("boom")
	}))
	rec := httptest.NewRecorder()

	// Action
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// Assert
	suite.Equal(http.StatusAccepted, rec.Code)
}

func (suite *TestMiddlewareSuite) Test_Recover_ErrAbortHandler() {
	// Arrange
	h := Recover()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	// Action & Assert
	suite.PanicsWithValue(http.ErrAbortHandler, func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func (suite *TestMiddlewareSuite) Test_AccessLog() {
	// Arrange
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})
	mux.HandleFunc("GET /fail", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	h := AccessLog()(mux)

	// Action
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/42", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", nil))

	// Assert
	lines := suite.logLines()
	suite.Require().Len(lines, 2)
	suite.Equal("INFO", lines[0]["level"])
	suite.Equal("/orders/42", lines[0]["path"])
	suite.Equal("GET /orders/{id}", lines[0]["route"])
	suite.InDelta(http.StatusCreated, lines[0]["status"], 0)
	suite.InDelta(5, lines[0]["bytes"], 0)
	suite.Equal("ERROR", lines[1]["level"])
	suite.InDelta(http.StatusBadGateway, lines[1]["status"], 0)
}

func (suite *TestMiddlewareSuite) Test_AccessLog_LogsRecoveredPanic() {
	// Arrange
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), AccessLog(), Recover())

	// Action
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	// Assert
	lines := suite.logLines()
	suite.Require().Len(lines, 2)
	suite.Equal("http request", lines[1]["msg"])
	suite.InDelta(http.StatusInternalServerError, lines[1]["status"], 0)
}

func (suite *TestMiddlewareSuite) Test_Metrics() {
	// Arrange
	reg := prometheus.NewRegistry()
	client := metrics.NewClientWithRegisterer("test", "test", reg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(http.ResponseWriter, *http.Request) {})
	h := Chain(mux, TraceID(), Metrics(client))

	// Action
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/1", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/2", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))

	// Assert
	families, err := reg.Gather()
	suite.Require().NoError(err)
	suite.Require().Len(families, 1)
	suite.Equal("test_http_server", families[0].GetName())

	counts := map[string]uint64{}
	for _, m := range families[0].GetMetric() {
		labels := map[string]string{}
		for _, l := range m.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		counts[labels["method"]+" "+labels["route"]+" "+labels["status"]] = m.GetHistogram().GetSampleCount()
	}
	suite.Equal(map[string]uint64{
		"GET GET /orders/{id} 200": 2,
		"GET unmatched 404":        1,
	}, counts)
}

func (suite *TestMiddlewareSuite) Test_ResponseWriter_Unwrap() {
	// Arrange
	h := AccessLog()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("chunk"))
		suite.NoError(http.NewResponseController(w).Flush())
	}))
	rec := httptest.NewRecorder()

	// Action
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// Assert
	suite.True(rec.Flushed)
}
