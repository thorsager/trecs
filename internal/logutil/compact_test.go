package logutil

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// stripAnsi removes ANSI color codes from a string for test assertions.
func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func TestCompactHandler(t *testing.T) {
	tests := []struct {
		name     string
		level    slog.Level
		msg      string
		attrs    []any
		want     string
		wantSub  []string
		notWant  []string
	}{
		{
			name:  "info log",
			level: slog.LevelInfo,
			msg:   "test message",
			want:  "[INFO] test message",
		},
		{
			name:    "log with attributes",
			level:   slog.LevelDebug,
			msg:     "debug info",
			attrs:   []any{"key", "value", "count", 42},
			wantSub: []string{"[DEBUG] debug info", "key=value", "count=42"},
		},
		{
			name:    "trace log without payload",
			level:   LevelTrace,
			msg:     "trace msg",
			attrs:   []any{"other", "data"},
			wantSub: []string{"[TRACE] trace msg", "other=data"},
			notWant: []string{"\t"},
		},
		{
			name:    "trace log with payload",
			level:   LevelTrace,
			msg:     "trace with payload",
			attrs:   []any{"payload", "SIP/2.0 INVITE sip:user@example.com", "other", "value"},
			wantSub: []string{"[TRACE] trace with payload", "other=value", "\tSIP/2.0 INVITE sip:user@example.com"},
		},
		{
			name:    "trace log with multiline payload",
			level:   LevelTrace,
			msg:     "trace multiline",
			attrs:   []any{"payload", "line1\nline2\nline3", "key", "val"},
			wantSub: []string{"[TRACE] trace multiline", "key=val", "\tline1", "\tline2", "\tline3"},
		},
		{
			name:  "error log",
			level: slog.LevelError,
			msg:   "something failed",
			attrs: []any{"error", "connection refused"},
			wantSub: []string{"[ERROR] something failed", "error=connection refused"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := NewCompactHandler(&buf, &CompactHandlerOptions{Level: LevelTrace})
			logger := slog.New(h)

			logger.Log(context.Background(), tt.level, tt.msg, tt.attrs...)

			output := stripAnsi(buf.String())

			// Check timestamp format (should be YYYY-MM-DD HH:MM:SS)
			parts := strings.SplitN(output, " ", 3)
			if len(parts) != 3 {
				t.Fatalf("expected timestamp (date time) followed by space, got: %q", output)
			}
			if !strings.Contains(parts[0], "-") || !strings.Contains(parts[1], ":") {
				t.Errorf("timestamp doesn't look like DateTime format: %q %q", parts[0], parts[1])
			}

			rest := parts[2]

			if tt.want != "" {
				if !strings.Contains(rest, tt.want) {
					t.Errorf("output %q does not contain %q", rest, tt.want)
				}
			}

			for _, sub := range tt.wantSub {
				if !strings.Contains(output, sub) {
					t.Errorf("output %q does not contain %q", output, sub)
				}
			}

			for _, sub := range tt.notWant {
				if strings.Contains(output, sub) {
					t.Errorf("output %q should not contain %q", output, sub)
				}
			}
		})
	}
}

func TestCompactHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf, &CompactHandlerOptions{Level: LevelTrace})
	h2 := h.WithAttrs([]slog.Attr{slog.String("persistent", "attr")})
	logger := slog.New(h2)

	logger.Info("test", "key", "value")

	output := stripAnsi(buf.String())
	if !strings.Contains(output, "persistent=attr") {
		t.Errorf("expected persistent attribute, got: %q", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected key=value, got: %q", output)
	}
}

func TestCompactHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf, &CompactHandlerOptions{Level: LevelTrace})
	h2 := h.WithGroup("mygroup")
	logger := slog.New(h2)

	logger.Info("test", "key", "value")

	output := stripAnsi(buf.String())
	if !strings.Contains(output, "mygroup.key=value") {
		t.Errorf("expected grouped attribute, got: %q", output)
	}
}

func TestCompactHandlerEnabled(t *testing.T) {
	h := NewCompactHandler(&bytes.Buffer{}, &CompactHandlerOptions{Level: slog.LevelWarn})

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected Info to be disabled when level is Warn")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("expected Error to be enabled when level is Warn")
	}
}
