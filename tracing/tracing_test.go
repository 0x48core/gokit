package tracing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/0x48core/gokit/logger"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const parentTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

type TestTracingSuite struct {
	suite.Suite
	recorder *tracetest.SpanRecorder
}

func TestTestTracingSuite(t *testing.T) {
	suite.Run(t, new(TestTracingSuite))
}

func (suite *TestTracingSuite) SetupTest() {
	suite.recorder = tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(suite.recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

func (suite *TestTracingSuite) TearDownTest() {
	otel.SetTracerProvider(noop.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
}

func (suite *TestTracingSuite) span() sdktrace.ReadOnlySpan {
	spans := suite.recorder.Ended()
	suite.Require().Len(spans, 1)
	return spans[0]
}

func attrs(span sdktrace.ReadOnlySpan) map[string]any {
	m := map[string]any{}
	for _, kv := range span.Attributes() {
		m[string(kv.Key)] = kv.Value.AsInterface()
	}
	return m
}

func (suite *TestTracingSuite) Test_Setup_Disabled() {
	// Arrange
	otel.SetTracerProvider(noop.NewTracerProvider())

	// Action
	shutdown, err := Setup(context.Background(), &Config{})

	// Assert
	suite.Require().NoError(err)
	suite.NoError(shutdown(context.Background()))
	suite.IsType(noop.TracerProvider{}, otel.GetTracerProvider())
	suite.ElementsMatch([]string{"traceparent", "tracestate", "baggage"}, otel.GetTextMapPropagator().Fields())
}

func (suite *TestTracingSuite) Test_Setup_ExportsToCollector() {
	// Arrange
	var exports atomic.Int32
	var auth atomic.Value
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			exports.Add(1)
			auth.Store(r.Header.Get("Authorization"))
		}
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer collector.Close()

	// Action
	shutdown, err := Setup(context.Background(), &Config{
		Enabled:     true,
		ServiceName: "orders",
		Endpoint:    collector.URL + "/v1/traces",
		Headers:     map[string]string{"Authorization": "Bearer token"},
	})
	suite.Require().NoError(err)
	_, span := Start(context.Background(), "work")
	span.End()
	suite.Require().NoError(shutdown(context.Background()))

	// Assert
	suite.Equal(int32(1), exports.Load())
	suite.Equal("Bearer token", auth.Load())
}

func (suite *TestTracingSuite) Test_ExporterOptions() {
	suite.Empty(exporterOptions(&Config{}))
	suite.Len(exporterOptions(&Config{Endpoint: "collector:4318", Insecure: true}), 2)
	suite.Len(exporterOptions(&Config{Endpoint: "http://collector:4318/v1/traces"}), 1)
}

func (suite *TestTracingSuite) Test_Start_SetsLoggerTraceID() {
	// Action
	ctx, span := Start(context.Background(), "work")
	span.End()

	// Assert
	suite.Equal(span.SpanContext().TraceID().String(), logger.GetTraceID(ctx))
	suite.Equal("work", suite.span().Name())
}

func (suite *TestTracingSuite) Test_Middleware_ContinuesTrace() {
	// Arrange
	var loggedTraceID string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		loggedTraceID = logger.GetTraceID(r.Context())
		w.WriteHeader(http.StatusCreated)
	})
	req := httptest.NewRequest(http.MethodGet, "http://orders.local:8080/orders/42", nil)
	req.Header.Set("traceparent", "00-"+parentTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()

	// Action
	Middleware()(mux).ServeHTTP(rec, req)

	// Assert
	span := suite.span()
	suite.Equal(parentTraceID, span.SpanContext().TraceID().String())
	suite.Equal("00f067aa0ba902b7", span.Parent().SpanID().String())
	suite.Equal("GET /orders/{id}", span.Name())
	suite.Equal(trace.SpanKindServer, span.SpanKind())
	suite.Equal(parentTraceID, loggedTraceID)
	suite.Equal(parentTraceID, rec.Header().Get(TraceIDHeader))

	a := attrs(span)
	suite.Equal("/orders/{id}", a[string(semconv.HTTPRouteKey)])
	suite.Equal("/orders/42", a[string(semconv.URLPathKey)])
	suite.Equal("orders.local", a[string(semconv.ServerAddressKey)])
	suite.Equal(int64(http.StatusCreated), a[string(semconv.HTTPResponseStatusCodeKey)])
	suite.Equal(codes.Unset, span.Status().Code)
}

func (suite *TestTracingSuite) Test_Middleware_ServerError() {
	// Arrange
	h := Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	// Action
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/", nil))

	// Assert
	span := suite.span()
	suite.Equal("_OTHER", span.Name())
	suite.Equal(codes.Error, span.Status().Code)
	suite.Nil(attrs(span)[string(semconv.HTTPRouteKey)])
}

func (suite *TestTracingSuite) Test_Middleware_Disabled_GeneratesTraceID() {
	// Arrange
	otel.SetTracerProvider(noop.NewTracerProvider())
	var loggedTraceID string
	h := Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		loggedTraceID = logger.GetTraceID(r.Context())
	}))
	rec := httptest.NewRecorder()

	// Action
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// Assert
	suite.Len(loggedTraceID, 32)
	suite.Equal(loggedTraceID, rec.Header().Get(TraceIDHeader))
}

func (suite *TestTracingSuite) Test_Transport_InjectsTraceContext() {
	// Arrange
	var traceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	client := &http.Client{Transport: Transport(nil)}
	ctx, parent := Start(context.Background(), "parent")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/items?token=secret", nil)
	suite.Require().NoError(err)

	// Action
	resp, err := client.Do(req)
	suite.Require().NoError(err)
	resp.Body.Close()
	parent.End()

	// Assert
	spans := suite.recorder.Ended()
	suite.Require().Len(spans, 2)
	span := spans[0]
	suite.Equal(trace.SpanKindClient, span.SpanKind())
	suite.Equal(parent.SpanContext().SpanID(), span.Parent().SpanID())
	suite.Equal("00-"+span.SpanContext().TraceID().String()+"-"+span.SpanContext().SpanID().String()+"-01", traceparent)
	suite.Equal(upstream.URL+"/items", attrs(span)[string(semconv.URLFullKey)])
	suite.Equal(codes.Error, span.Status().Code)
	suite.Empty(req.Header.Get("traceparent"), "the caller's request must not be modified")
}

func (suite *TestTracingSuite) Test_Transport_Error() {
	// Arrange
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	client := &http.Client{Transport: Transport(nil)}

	// Action
	_, err := client.Get(upstream.URL)

	// Assert
	suite.Require().Error(err)
	span := suite.span()
	suite.Equal(codes.Error, span.Status().Code)
	suite.Len(span.Events(), 1)
}
