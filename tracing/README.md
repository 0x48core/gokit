# Tracing

OpenTelemetry tracing setup with an OTLP/HTTP exporter, plus HTTP server middleware and a client
transport, built on [opentelemetry-go](https://github.com/open-telemetry/opentelemetry-go).

- One `Setup` call installs the global tracer provider and the W3C `traceparent` / `baggage`
  propagator
- The [logger](../logger) trace ID is set to the OpenTelemetry trace ID, so log lines and traces
  share one ID
- Server spans named after the `ServeMux` route, such as `GET /orders/{id}`
- Client spans that send the trace context to downstream services
- When disabled, nothing is exported but trace context still passes through

## Installation

```sh
go get github.com/0x48core/gokit/tracing
```

Requires Go 1.25+.

## Usage

```go
func main() {
	logger.SetupLogger()

	shutdown, err := tracing.Setup(context.Background(), &tracing.Config{
		Enabled:     true,
		ServiceName: "orders",
		Environment: "production",
		Endpoint:    "otel-collector:4318",
		Insecure:    true,
		SampleRatio: 0.1,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer shutdown(context.Background()) // flushes the last spans

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", getOrder)

	handler := httpserver.Chain(mux,
		tracing.Middleware(), // in place of httpserver.TraceID()
		httpserver.AccessLog(),
		httpserver.Metrics(metricClient),
		httpserver.Recover(),
	)
	// ...
}
```

Spans for your own work:

```go
func (s *Service) Reserve(ctx context.Context, orderID string) (err error) {
	ctx, span := tracing.Start(ctx, "Service.Reserve",
		trace.WithAttributes(attribute.String("order.id", orderID)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	slog.InfoContext(ctx, "reserving stock") // logs the span's trace ID
	// ...
}
```

Calls to other services:

```go
client := &http.Client{Transport: tracing.Transport(nil), Timeout: 5 * time.Second}
```

### Config

`Config` has `mapstructure` tags, so it loads with [config](../config).

| Field | Key | Description |
| --- | --- | --- |
| `Enabled` | `enabled` | Export spans. When false, `Setup` only installs the propagator. |
| `ServiceName` | `service_name` | The `service.name` resource attribute. Falls back to `OTEL_SERVICE_NAME`. |
| `Environment` | `environment` | The `deployment.environment.name` resource attribute. |
| `Endpoint` | `endpoint` | Collector address as `host:port` (HTTPS), or a URL such as `http://localhost:4318/v1/traces`. When empty, the `OTEL_EXPORTER_OTLP_*` variables apply, then `localhost:4318`. |
| `Insecure` | `insecure` | Use plain HTTP for a `host:port` endpoint. |
| `Headers` | `headers` | Headers sent with every export, for example `Authorization`. |
| `SampleRatio` | `sample_ratio` | Share of new traces recorded, from 0 to 1. `0` means `1`. |

Sampling is parent based: a request whose `traceparent` is sampled is always recorded, and one
that is not sampled never is, whatever the ratio. `OTEL_RESOURCE_ATTRIBUTES` adds resource
attributes.

### Middleware

`Middleware()` starts a server span for each request, continuing the trace in its `traceparent`
header. It sets the logger trace ID to the span's trace ID and returns it in `X-Trace-Id`. It
does the same job as `httpserver.TraceID`, so use one or the other. When tracing is disabled and
the request has no `traceparent`, it generates a trace ID as `httpserver.TraceID` does.

The span records `http.request.method`, `url.path`, `url.scheme`, `server.address`,
`user_agent.original`, `http.route` and `http.response.status_code`. 5xx responses mark the span
as an error. Methods outside the standard set are named `_OTHER`.

Put it before middleware that log or record metrics, so they see its trace ID. Like
`httpserver.Metrics`, it reads the route from the request it passes on, so middleware between it
and the mux must not replace the request.

### Transport

`Transport(base)` wraps a `RoundTripper`, `http.DefaultTransport` when `base` is `nil`. Each
request gets a client span, a child of the span in the request's context, and the trace context
in its headers. The span records the URL without its query string or user info, which may hold
credentials. 4xx and 5xx responses and transport errors mark the span as an error. The span ends
when the response headers arrive, so it does not include reading the body.

## API

| Name | Description |
| --- | --- |
| `Setup(ctx, conf)` | Installs the propagator and, when enabled, the tracer provider. Returns a `Shutdown` to flush spans. |
| `Start(ctx, name, opts...)` | Starts a span and sets the logger trace ID to its trace ID. |
| `Middleware()` | HTTP server middleware that starts a span per request. |
| `Transport(base)` | `RoundTripper` that starts a span per request and propagates the trace context. |
| `TraceIDHeader` | Response header for the trace ID, `X-Trace-Id`. |

## Notes

- **Call `Shutdown` before exiting.** Spans are exported in batches, so the last ones are lost
  otherwise.
- **`Setup` sets global state.** Call it once, at startup. Spans started before it are not
  exported.
- **Only the trace ID reaches the logs.** The logger has no span ID field, so use the trace ID to
  find a request's spans.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
