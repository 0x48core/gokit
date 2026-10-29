# Health

Liveness and readiness HTTP handlers for Kubernetes probes and load balancers, with no
dependencies outside the standard library.

- Readiness runs every check concurrently, each with its own timeout
- Liveness runs no checks, so a broken dependency doesn't get the process restarted
- `Drain` fails readiness during shutdown, so traffic stops before the server does
- Failures are logged when a check starts and stops failing, not on every probe
- Checks are `func(context.Context) error`, so methods such as redis `Client.Ping` and
  `sql.DB.PingContext` fit as-is

## Installation

```sh
go get github.com/0x48core/gokit/health
```

Requires Go 1.25+.

## Usage

```go
checker := health.New(
	health.WithCheck("redis", redisClient.Ping),
	health.WithCheck("postgres", db.PingContext),
	health.WithTimeout(time.Second),
)

mux := http.NewServeMux()
mux.Handle("GET /livez", checker.Liveness())
mux.Handle("GET /readyz", checker.Readiness())
```

`GET /readyz` responds `200` when every check passes and `503` otherwise:

```json
{
  "status": "fail",
  "checks": {
    "postgres": { "status": "ok", "duration_ms": 2 },
    "redis": { "status": "fail", "error": "dial tcp 10.0.0.5:6379: connect: connection refused", "duration_ms": 1 }
  }
}
```

`GET /livez` always responds `200` with `{"status":"ok"}`.

A check can be any function:

```go
health.WithCheck("kafka", func(ctx context.Context) error {
	_, err := adminClient.GetMetadata(nil, false, 1000)
	return err
})
```

Use `Register(name, check)` to add a check after `New`.

### Graceful shutdown

Kubernetes keeps sending traffic to a pod for a few seconds after it sends `SIGTERM`. Call `Drain`
first, wait for load balancers to notice, then shut the server down:

```go
sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

serverCtx, stopServer := context.WithCancel(context.Background())
go func() {
	<-sigCtx.Done()
	checker.Drain()             // /readyz now responds 503
	time.Sleep(5 * time.Second) // longer than the readiness probe period
	stopServer()
}()

if err := httpserver.New(conf, handler).Run(serverCtx); err != nil {
	slog.Error("http server stopped", "error", err)
}
```

After `Drain`, readiness responds `503` without running the checks. Liveness keeps responding
`200`.

### Logging

When a check fails after passing, or on its first run, `health check failing` is logged at warn
level with the check name and error. When it passes again, `health check recovered` is logged at
info level. Repeated probes with the same result log nothing.

## API

| Name | Description |
| --- | --- |
| `New(opts...)` | Creates a checker. |
| `WithCheck(name, check)` | Adds a check that readiness runs. |
| `WithTimeout(d)` | Sets how long each check may take. Default `2s`. |
| `(*Checker).Register(name, check)` | Adds a check after `New`. |
| `(*Checker).Liveness()` | Handler that always responds `200`. |
| `(*Checker).Readiness()` | Handler that runs the checks and responds `200` or `503`. |
| `(*Checker).Check(ctx)` | Runs the checks and returns the `Result`, for use outside HTTP. |
| `(*Checker).Drain()` | Makes readiness fail from now on. |
| `Check` | `func(ctx context.Context) error` |

## Notes

- **Readiness shows check errors.** Error messages can name hosts and ports, so serve the probe
  endpoints on an internal port or keep them off public routes.
- **Checks should respect `ctx`.** A check that ignores it fails at the timeout, but its goroutine
  runs until the check returns.
- **Use unique names.** Two checks with the same name both run, but only one shows in the result.
- **Every probe runs the checks.** Keep them cheap, such as a ping, and the probe period at a few
  seconds.

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
