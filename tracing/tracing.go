package tracing

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x48core/gokit/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/0x48core/gokit/tracing"

type Config struct {
	// Enabled turns on exporting. When false, Setup only installs the W3C
	// propagator, so trace context still passes through the service.
	Enabled     bool   `mapstructure:"enabled"`
	ServiceName string `mapstructure:"service_name"`
	Environment string `mapstructure:"environment"`
	// Endpoint is the collector's OTLP/HTTP address, as host:port (HTTPS) or
	// a URL such as http://localhost:4318/v1/traces. When empty, the
	// OTEL_EXPORTER_OTLP_* environment variables apply, then localhost:4318.
	Endpoint string `mapstructure:"endpoint"`
	// Insecure sends to a host:port Endpoint over plain HTTP.
	Insecure bool `mapstructure:"insecure"`
	// Headers are sent with every export, for example for authentication.
	Headers map[string]string `mapstructure:"headers"`
	// SampleRatio is the share of new traces that are recorded, from 0 to 1.
	// 0 means 1. Spans with a remote parent follow the parent's decision.
	SampleRatio float64 `mapstructure:"sample_ratio"`
}

// Shutdown flushes buffered spans and stops the exporter.
type Shutdown func(context.Context) error

// Setup installs the W3C trace context and baggage propagator and, when
// conf.Enabled, a global tracer provider that batches spans to an OTLP/HTTP
// collector. Call the returned Shutdown before the process exits, or the last
// spans are lost.
func Setup(ctx context.Context, conf *Config) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	if conf == nil || !conf.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx, exporterOptions(conf)...)
	if err != nil {
		return nil, fmt.Errorf("tracing: create exporter: %w", err)
	}

	attrs := []attribute.KeyValue{}
	if conf.ServiceName != "" {
		attrs = append(attrs, semconv.ServiceName(conf.ServiceName))
	}
	if conf.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(conf.Environment))
	}
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
		resource.WithAttributes(attrs...))
	if err != nil {
		return nil, fmt.Errorf("tracing: create resource: %w", err)
	}

	ratio := conf.SampleRatio
	if ratio == 0 {
		ratio = 1
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))))
	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

func exporterOptions(conf *Config) []otlptracehttp.Option {
	var opts []otlptracehttp.Option
	switch {
	case strings.Contains(conf.Endpoint, "://"):
		opts = append(opts, otlptracehttp.WithEndpointURL(conf.Endpoint))
	case conf.Endpoint != "":
		opts = append(opts, otlptracehttp.WithEndpoint(conf.Endpoint))
	}
	if conf.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(conf.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(conf.Headers))
	}
	return opts
}

// Start starts a span from the global tracer provider and sets the logger
// trace ID to the span's trace ID, so slog calls made with the returned
// context log it. End the span when the work is done.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, name, opts...)
	if sc := span.SpanContext(); sc.HasTraceID() {
		ctx = logger.WithTraceID(ctx, sc.TraceID().String())
	}
	return ctx, span
}
