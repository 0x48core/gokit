# Logger

Structured logging in Go with [`log/slog`](https://pkg.go.dev/log/slog).

- JSON output to stdout, with short source locations (`file.go:line`)
- Trace ID carried through `context.Context` and attached to every log record as `trace_id`
- Arbitrary key/value pairs stored in the context and added to every log record

## Installation

```sh
go get github.com/0x48core/gokit/logger
```

Requires Go 1.25+.

## Usage

```go
package main

import (
	"context"
	"log/slog"

	"github.com/0x48core/gokit/logger"
)

func main() {
	// Install the logger as the slog default.
	logger.SetupLogger()

	ctx := context.Background()

	// Attach a trace ID (or generate one with logger.GenerateTraceID).
	ctx = logger.WithTraceID(ctx, "trace_123")

	// Attach key/values that will be added to every log written with this ctx.
	ctx = logger.WithKeyValues(ctx, "user_id", 42, "service", "billing")

	slog.InfoContext(ctx, "payment processed", "amount", 100)
}
```

Output:

```json
{"time":"...","level":"INFO","source":{"function":"main.main","file":"main.go","line":21},"msg":"payment processed","amount":100,"trace_id":"trace_123","user_id":42,"service":"billing"}
```

> Use the `*Context` variants (`slog.InfoContext`, `slog.ErrorContext`, ...) so the trace ID and context values are picked up.

## API

### Setup

| Function | Description |
| --- | --- |
| `SetupLogger()` | Sets a JSON `slog` handler on stdout as the default logger. |
| `TraceIDKey` | Attribute key used for the trace ID (default `"trace_id"`). |
| `TransformContext` | Hook applied to the context before reading or writing values. Override it to adapt contexts from frameworks (for example, to unwrap a framework's request context). |

### Trace ID

| Function | Description |
| --- | --- |
| `WithTraceID(ctx, traceID)` | Returns a context carrying `traceID`. |
| `GetTraceID(ctx)` | Returns the trace ID, or `""` if none is set. |
| `GenerateTraceID(ctx)` | Generates a UUID trace ID with no dashes and returns the new context and the ID. |

### Context values

| Function | Description |
| --- | --- |
| `WithKeyValues(ctx, k1, v1, k2, v2, ...)` | Appends key/value pairs that are added to every log record. |
| `GetKeyValues(ctx)` | Returns the key/value pairs added with `WithKeyValues`. |
| `SetKeyValues(ctx, k1, v1, ...)` | Stores values in a per-context map (keys must be `string`). These values are **not** logged automatically. |
| `GetKey(ctx, key)` | Reads a value stored with `SetKeyValues`. |

## Development

```sh
make fmt   # goimports, gofumpt, gci
make lint  # golangci-lint + go vet
make test  # go test with race detector and coverage
```
