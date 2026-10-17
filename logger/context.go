package logger

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

var TransformContext = func(ctx context.Context) context.Context {
	return ctx
}

type ctxKeyTraceID struct{}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(TransformContext(ctx), ctxKeyTraceID{}, traceID)
}

func GetTraceID(ctx context.Context) string {
	traceID, ok := (TransformContext(ctx).Value(ctxKeyTraceID{})).(string)
	if !ok {
		return ""
	}
	return traceID
}

func GenerateTraceID(ctx context.Context) (context.Context, string) {
	traceID := uuid.NewString()
	traceID = strings.ReplaceAll(traceID, "-", "")
	return WithTraceID(ctx, traceID), traceID
}

type ctxKeyAttrs struct{}

func WithKeyValues(ctx context.Context, keyAndValues ...any) context.Context {
	ctx = TransformContext(ctx)
	length := len(keyAndValues)
	if length%2 != 0 {
		keyAndValues = keyAndValues[:length-1]
	}

	if length == 0 {
		return ctx
	}

	data, ok := ctx.Value(ctxKeyAttrs{}).([]any)
	if !ok {
		data = []any{}
	}
	data = append(data, keyAndValues...)

	return context.WithValue(ctx, ctxKeyAttrs{}, data)
}

func GetKeyValues(ctx context.Context) []any {
	data, ok := TransformContext(ctx).Value(ctxKeyAttrs{}).([]any)
	if ok {
		return data
	}
	return nil
}

type ctxMap struct{}

func SetKeyValues(ctx context.Context, keyAndValues ...any) context.Context {
	ctx = TransformContext(ctx)
	length := len(keyAndValues)
	if length%2 != 0 {
		keyAndValues = keyAndValues[:length-1]
	}

	if length == 0 {
		return ctx
	}

	data, ok := ctx.Value(ctxMap{}).(map[string]any)
	if !ok {
		data = map[string]any{}
	}
	for i := 0; i < length; i += 2 {
		data[keyAndValues[i].(string)] = keyAndValues[i+1]
	}
	return context.WithValue(ctx, ctxMap{}, data)
}

func GetKey(ctx context.Context, key string) any {
	data, ok := TransformContext(ctx).Value(ctxMap{}).(map[string]any)
	if ok {
		return data[key]
	}
	return nil
}
