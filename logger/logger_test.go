package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestLogger(t *testing.T) {
	SetupLogger()

	ctx := context.Background()
	ctx = WithTraceID(ctx, "trace_123")
	ctx = WithKeyValues(ctx, "name", "Test Logger")
	slog.InfoContext(ctx, "Hello, word 1")

	ctx = SetKeyValues(ctx, "age", 12)
	age := GetKey(ctx, "age")
	slog.InfoContext(ctx, "Hello, word 2", "age", age)
}
