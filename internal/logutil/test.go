package logutil

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestHandler is a slog.Handler that routes log output to testing.TB.Log.
type TestHandler struct {
	tb    testing.TB
	attrs []slog.Attr
	group string
}

// NewTestLogger returns a *slog.Logger that writes to t.Log / t.Logf.
func NewTestLogger(tb testing.TB) *slog.Logger {
	return slog.New(&TestHandler{tb: tb})
}

func (h *TestHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= LevelTrace
}

func (h *TestHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder

	if h.group != "" {
		sb.WriteString(h.group)
		sb.WriteString(": ")
	}

	sb.WriteString(levelString(r.Level))
	sb.WriteString(" ")
	sb.WriteString(r.Message)

	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(formatAttr(a, h.group))
		return true
	})

	for _, a := range h.attrs {
		sb.WriteString(" ")
		sb.WriteString(formatAttr(a, h.group))
	}

	h.tb.Log(sb.String())
	return nil
}

// Logger wraps *slog.Logger with a Trace method.
type Logger struct {
	*slog.Logger
}

// NewLogger creates a Logger wrapping *slog.Logger.
func NewLogger(l *slog.Logger) *Logger {
	return &Logger{Logger: l}
}

// Trace logs at LevelTrace.
func (l *Logger) Trace(msg string, args ...any) {
	if l.Enabled(context.Background(), LevelTrace) {
		l.Logger.Log(context.Background(), LevelTrace, msg, args...)
	}
}

// TraceCtx logs at LevelTrace with a context.
func (l *Logger) TraceCtx(ctx context.Context, msg string, args ...any) {
	if l.Enabled(ctx, LevelTrace) {
		l.Logger.Log(ctx, LevelTrace, msg, args...)
	}
}

// Trace logs at LevelTrace if the logger supports it.
func Trace(logger *slog.Logger, msg string, args ...any) {
	if logger.Enabled(context.Background(), LevelTrace) {
		logger.Log(context.Background(), LevelTrace, msg, args...)
	}
}

// TraceCtx logs at LevelTrace with a context (for trace ID propagation).
func TraceCtx(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	if logger.Enabled(ctx, LevelTrace) {
		logger.Log(ctx, LevelTrace, msg, args...)
	}
}

func levelString(level slog.Level) string {
	switch level {
	case LevelTrace:
		return "TRACE"
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return level.String()
	}
}

func (h *TestHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &TestHandler{tb: h.tb, attrs: newAttrs, group: h.group}
}

func (h *TestHandler) WithGroup(name string) slog.Handler {
	return &TestHandler{tb: h.tb, attrs: h.attrs, group: name}
}

func formatAttr(a slog.Attr, group string) string {
	if a.Value.Kind() == slog.KindGroup {
		var parts []string
		for _, ga := range a.Value.Group() {
			parts = append(parts, formatAttr(ga, a.Key))
		}
		return strings.Join(parts, " ")
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	return fmt.Sprintf("%s=%v", key, a.Value.Any())
}
