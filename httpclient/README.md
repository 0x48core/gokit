# HTTP Client

An `*http.Client` with safe timeouts, a pooled transport, retries with backoff, trace ID
propagation and per-host latency metrics.

- A standard `*http.Client`, so it works with any SDK or code that takes one
- Timeouts for the whole call, dialing and TLS, and a connection pool sized for services
- Retries with exponential backoff and jitter, only for idempotent requests, honouring
  `Retry-After`
- The [logger](../logger) trace ID sent in `X-Trace-Id`, so the server's logs share it
- Each call's latency recorded in an `http_client` histogram from [metrics](../metrics), tagged
  with the method, host and status
- Transport middleware, such as [tracing](../tracing)`.Transport`, run on every attempt

## Installation

```sh
go get github.com/0x48core/gokit/httpclient
```

Requires Go 1.25+.

## Usage

```go
client := httpclient.New(&httpclient.Config{
	Timeout:    5 * time.Second,
	MaxRetries: 2,
}, metricClient, httpclient.WithMiddleware(tracing.Transport))

req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://inventory/items/42", nil)
if err != nil {
	return err
}
resp, err := client.Do(req)
if err != nil {
	return err
}
defer resp.Body.Close()
```

Always create requests with the caller's context. It carries the trace ID and span, and
cancelling it stops the call and any pending retry.

### Config

`Config` has `mapstructure` tags, so it loads with [config](../config). Zero fields get the
defaults:

| Field | Key | Default |
| --- | --- | --- |
| `Timeout` | `timeout` | `10s` for the whole call, retries and body included |
| `DialTimeout` | `dial_timeout` | `5s` |
| `TLSHandshakeTimeout` | `tls_handshake_timeout` | `5s` |
| `IdleConnTimeout` | `idle_conn_timeout` | `90s` |
| `MaxIdleConnsPerHost` | `max_idle_conns_per_host` | `100` (net/http's default is 2) |
| `MaxConnsPerHost` | `max_conns_per_host` | `0`, no limit |
| `MaxRetries` | `max_retries` | `0`, no retries |
| `RetryWaitMin` | `retry_wait_min` | `100ms` |
| `RetryWaitMax` | `retry_wait_max` | `2s` |

### Retries

With `MaxRetries` above 0, a request is sent again after a transport error or a `429`, `502`,
`503` or `504` response, up to `MaxRetries` more times. A `500` is not retried, since the server
may have done the work.

Only requests that are safe to send twice are retried:

- `GET`, `HEAD`, `OPTIONS`, `TRACE`, `PUT` and `DELETE`, or any method with an `Idempotency-Key`
  header
- with no body, or a body the client can replay. `http.NewRequest` makes bodies from
  `bytes.Buffer`, `bytes.Reader` and `strings.Reader` replayable. Set `req.GetBody` for others.

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
req.Header.Set(httpclient.IdempotencyKeyHeader, orderID) // POST is retried only with a key
```

Attempt `n` waits a random time between `RetryWaitMin` and `RetryWaitMin × 2ⁿ`, capped at
`RetryWaitMax`. When the response has a `Retry-After` in seconds, the client waits that long
instead, also capped at `RetryWaitMax`. After the last attempt, the last response or error is
returned as it is.

### Middleware

`WithMiddleware(mws...)` wraps the transport inside the retries, so each middleware runs once per
attempt, the first one outermost. Any `func(http.RoundTripper) http.RoundTripper` fits. With
`tracing.Transport`, each attempt gets its own client span and the downstream service gets the
`traceparent` header.

`WithTransport(rt)` replaces the transport built from `Config`, for example to set custom TLS
options. The pool settings in `Config` then don't apply, but the timeout, retries, trace ID and
metrics do.

### Metrics

Each call records its latency in seconds in `<namespace>_http_client`, tagged `method`, `host`
and `status`. `status` is the final response's code, or `error` when there was none. A call with
retries records one sample covering all its attempts. Pass a `nil` metrics client to record
nothing.

## API

| Name | Description |
| --- | --- |
| `New(conf, metricClient, opts...)` | Creates an `*http.Client`. `conf` and `metricClient` may be `nil`. |
| `WithMiddleware(mws...)` | Wraps every attempt with transport middleware. |
| `WithTransport(rt)` | Replaces the base transport. |
| `Middleware` | `func(http.RoundTripper) http.RoundTripper` |
| `TraceIDHeader` | Request header for the trace ID, `X-Trace-Id`. |
| `IdempotencyKeyHeader` | `Idempotency-Key`, which makes any method retryable. |

## Notes

- **Close the response body**, even when you don't read it, or the connection isn't reused.
- **`Timeout` covers retries.** Set it longer than the attempts and waits you allow, or the last
  retries never run.
- **The `host` tag is the URL's host.** Calling many different hosts, such as URLs taken from
  user input, creates a metric series per host.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
