package logx

import (
	"context"

	"go.uber.org/zap"
)

type ctxKey struct{}

// Trace 是随 context 贯穿全链路的追踪字段。
type Trace struct {
	TraceID   string
	RequestID string
	UserID    int64
}

// WithTrace 将追踪字段写入 context，供下游各层取用。
func WithTrace(ctx context.Context, t Trace) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// TraceOf 从 context 取出追踪字段，缺省返回零值。
func TraceOf(ctx context.Context) Trace {
	if ctx == nil {
		return Trace{}
	}
	if t, ok := ctx.Value(ctxKey{}).(Trace); ok {
		return t
	}
	return Trace{}
}

// TraceIDFrom 返回 context 中的 traceId，用于注入 Kafka 事件与内部 HTTP 头。
func TraceIDFrom(ctx context.Context) string { return TraceOf(ctx).TraceID }

// From 返回携带 context 内 traceId / requestId / userId 的 logger。
// 各层在请求路径中统一用它取 logger，traceId 即自动随日志输出。
func From(ctx context.Context) *zap.Logger {
	t := TraceOf(ctx)
	fields := make([]zap.Field, 0, 3)
	if t.TraceID != "" {
		fields = append(fields, zap.String("traceId", t.TraceID))
	}
	if t.RequestID != "" {
		fields = append(fields, zap.String("requestId", t.RequestID))
	}
	if t.UserID != 0 {
		fields = append(fields, zap.Int64("userId", t.UserID))
	}
	if len(fields) == 0 {
		return base
	}
	return base.With(fields...)
}
