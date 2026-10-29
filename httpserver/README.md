# HTTP Server

A `net/http` server with graceful shutdown, plus middleware for trace IDs, panic recovery, access
logs and latency metrics.

- `Run` serves until its context is done, then waits for in-flight requests before returning
- Timeouts with safe defaults, so a slow client cannot hold a connection open forever
- Trace IDs read from or added to `X-Trace-Id` and stored with [logger](../logger), so every log line
  of a request carries it
- Each request's latency recorded in an `http_server` histogram from [metrics](../metrics), tagged
  with the method, the `ServeMux` route pattern and the status code
- Middleware are plain `func(http.Handler) http.Handler`, so they work with any router

## Installation

```sh
go get github.com/0x48core/gokit/httpserver
```

Requires Go 1.25+.

## Usage

```go
func main() {
	logger.SetupLogger()
	metricClient := metrics.NewClient("orders", "production")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", getOrder)
	mux.Handle("GET /metrics", promhttp.Handler())

	handler := httpserver.Chain(mux,
		httpserver.TraceID(),
		httpserver.AccessLog(),
		httpserver.Metrics(metricClient),
		httpserver.Recover(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := httpserver.New(&httpserver.Config{Addr: ":8080"}, handler)
	if err := server.Run(ctx); err != nil {
		slog.Error("http server stopped", "error", err)
		os.Exit(1)
	}
}
```

On `SIGTERM`, `Run` stops accepting connections, waits up to `ShutdownTimeout` for in-flight
requests and returns `nil`. If requests are still running after the timeout, their connections are
closed and `Run` returns an error that wraps `context.DeadlineExceeded`.

`Serve(ctx, listener)` does the same on a listener you opened, for example on port 0 in tests.

### Config

`Config` has `mapstructure` tags, so it loads with [config](../config). Zero fields get the
defaults:

| Field | Key | Default |
| --- | --- | --- |
| `Addr` | `addr` | `:8080` |
| `ReadTimeout` | `read_timeout` | `10s` |
| `ReadHeaderTimeout` | `read_header_timeout` | `5s` |
| `WriteTimeout` | `write_timeout` | `10s` |
| `IdleTimeout` | `idle_timeout` | `60s` |
| `ShutdownTimeout` | `shutdown_timeout` | `15s` |

`WriteTimeout` limits the whole response, so raise it for streaming or long downloads.

### Middleware

`Chain(h, a, b, c)` runs `a`, then `b`, then `c`, then `h`. Use the order in the example:

1. `TraceID` first, so the log lines of all the others carry the trace ID.
2. `AccessLog` and `Metrics` next. They pass the request on unchanged, which lets them read the
   route pattern that `ServeMux` sets on it.
3. `Recover` last, so a panic becomes a 500 that `AccessLog` and `Metrics` still record.

| Middleware | Description |
| --- | --- |
| `TraceID()` | Reads the trace ID from `X-Trace-Id`, or generates one when it is missing or not up to 64 letters, digits, `-` and `_`. Stores it with `logger.WithTraceID` and sets it on the response. |
| `AccessLog()` | Logs `http request` with method, path, route, status, bytes, duration, remote address and user agent. 5xx responses log at error level, others at info. |
| `Metrics(client)` | Records the latency in seconds in `<namespace>_http_server`, tagged `method`, `route` and `status`. |
| `Recover()` | Logs a panic with its stack and responds 500 if nothing was written yet. `http.ErrAbortHandler` is re-panicked. |

Change `httpserver.TraceIDHeader` to use another header name.

## API

| Name | Description |
| --- | --- |
| `New(conf, handler)` | Creates a server. `conf` may be `nil` to use the defaults. |
| `(*Server).Run(ctx)` | Listens on `Addr` and serves until `ctx` is done, then shuts down gracefully. |
| `(*Server).Serve(ctx, ln)` | Like `Run`, on an existing listener. |
| `Chain(h, mws...)` | Wraps `h` with middleware, the first one outermost. |
| `Middleware` | `func(http.Handler) http.Handler` |

## Notes

- **Routes come from `ServeMux` patterns.** Requests that match no pattern are tagged
  `route="unmatched"`, so random paths don't create new metric series. With another router, every
  request is `unmatched`, so only the method and status tags are useful.
- **Middleware that replaces the request hides the route.** If a middleware between `Metrics` and
  the mux calls `r.WithContext`, the mux sets the pattern on the copy and `Metrics` sees
  `unmatched`. Put such middleware before `AccessLog` and `Metrics`, as `TraceID` is.
- **The server's own errors go to `slog.Default()`** as it was when `New` was called, so call
  `logger.SetupLogger` first.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
