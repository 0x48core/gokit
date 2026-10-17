package logger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
)

var TraceIDKey = "trace_id"

type LogHandler struct {
	defaultHandler slog.Handler
}

func SetupLogger() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				source, ok := a.Value.Any().(*slog.Source)
				if ok {
					source.File = filepath.Base(source.File)
					source.Function = filepath.Base(source.Function)
				}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(&LogHandler{defaultHandler: jsonHandler}))
}

func (h *LogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.defaultHandler.Enabled(ctx, level)
}

func (h *LogHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(slog.Attr{
		Key:   TraceIDKey,
		Value: slog.AnyValue(GetTraceID(ctx)),
	})

	record.Add(GetKeyValues(ctx)...)
	return h.defaultHandler.Handle(ctx, record)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.defaultHandler.WithAttrs(attrs)
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return h.defaultHandler.WithGroup(name)
}
