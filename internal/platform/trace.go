package platform

import "context"

type ctxKey int

const traceKey ctxKey = 1

// WithTraceID 注入 trace id。
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey, id)
}

// TraceID 读取 trace id（无则空串）。
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey).(string); ok {
		return v
	}
	return ""
}
