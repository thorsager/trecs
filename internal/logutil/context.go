package logutil

import (
	"context"
	"log/slog"
)

// LevelTrace is a custom log level for tracing SIP message I/O.
// It is more verbose than Debug (-4).
const LevelTrace = slog.Level(-10)

type loggerKey struct{}
type ctxValuesKey struct{}

// ctxValues holds key-value pairs stored in the context for automatic logging.
type ctxValues struct {
	attrs []slog.Attr
}

// FromContext returns a logger that automatically includes all context values
// (set via WithValue) as attributes on every log record.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return slog.New(&ctxAttrHandler{
			base:   logger.Handler(),
			ctx:    ctx,
			values: extractCtxValues(ctx),
		})
	}
	return slog.Default()
}

// NewContext stores a logger in the context.
func NewContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// WithValue stores a key-value pair in the context that will be automatically
// included as an attribute on every log record produced by FromContext(ctx).
func WithValue(ctx context.Context, key string, value any) context.Context {
	v := extractCtxValues(ctx)
	v.attrs = append(v.attrs, slog.Any(key, value))
	return context.WithValue(ctx, ctxValuesKey{}, v)
}

// WithValues stores multiple key-value pairs in the context for automatic logging.
func WithValues(ctx context.Context, kvs ...any) context.Context {
	v := extractCtxValues(ctx)
	for i := 0; i+1 < len(kvs); i += 2 {
		if key, ok := kvs[i].(string); ok {
			v.attrs = append(v.attrs, slog.Any(key, kvs[i+1]))
		}
	}
	return context.WithValue(ctx, ctxValuesKey{}, v)
}

func extractCtxValues(ctx context.Context) *ctxValues {
	if v, ok := ctx.Value(ctxValuesKey{}).(*ctxValues); ok {
		return v
	}
	return &ctxValues{}
}

// ctxAttrHandler wraps a slog.Handler and injects context values as attributes.
type ctxAttrHandler struct {
	base   slog.Handler
	ctx    context.Context
	values *ctxValues
}

func (h *ctxAttrHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *ctxAttrHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, a := range h.values.attrs {
		r.AddAttrs(a)
	}
	return h.base.Handle(ctx, r)
}

func (h *ctxAttrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxAttrHandler{
		base:   h.base.WithAttrs(attrs),
		ctx:    h.ctx,
		values: h.values,
	}
}

func (h *ctxAttrHandler) WithGroup(name string) slog.Handler {
	return &ctxAttrHandler{
		base:   h.base.WithGroup(name),
		ctx:    h.ctx,
		values: h.values,
	}
}
