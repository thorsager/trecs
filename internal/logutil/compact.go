package logutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	colorReset       = "\033[0m"
	colorCyan        = "\033[36m"
	colorGray        = "\033[90m"
	colorBrightWhite = "\033[97m"
	colorYellow      = "\033[33m"
	colorRed         = "\033[31m"
	colorDarkGreen   = "\033[32m"
	colorBlue        = "\033[34m"
)

// CompactHandler is a slog.Handler that formats logs as:
//
//	$time [$level] $msg key=value key=value ...
//
// For TRACE level, the "payload" attribute value is printed on a separate line
// indented with one tab.
//
// When writing to an interactive terminal, key=value pairs are colored cyan
// and TRACE payload lines are colored dark gray.
type CompactHandler struct {
	w      io.Writer
	opts   CompactHandlerOptions
	colors bool

	mu     sync.Mutex
	attrs  []slog.Attr
	groups []string
}

// CompactHandlerOptions holds options for CompactHandler.
type CompactHandlerOptions struct {
	Level slog.Leveler
}

// NewCompactHandler creates a new CompactHandler that writes to w.
func NewCompactHandler(w io.Writer, opts *CompactHandlerOptions) *CompactHandler {
	h := &CompactHandler{
		w: w,
	}
	if opts != nil {
		h.opts = *opts
	}
	if h.opts.Level == nil {
		h.opts.Level = slog.LevelInfo
	}

	// Detect if output is an interactive terminal
	if f, ok := w.(*os.File); ok {
		h.colors = term.IsTerminal(int(f.Fd()))
	}

	return h
}

// Enabled implements slog.Handler.
func (h *CompactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle implements slog.Handler.
func (h *CompactHandler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder

	// Time
	sb.WriteString(r.Time.Format(time.DateTime))
	sb.WriteString(" ")

	// Level
	sb.WriteString("[")
	if h.colors {
		sb.WriteString(levelColor(r.Level))
	}
	sb.WriteString(levelString(r.Level))
	if h.colors {
		sb.WriteString(colorReset)
	}
	sb.WriteString("] ")

	// Message
	if h.colors {
		sb.WriteString(colorBrightWhite)
	}
	sb.WriteString(r.Message)
	if h.colors {
		sb.WriteString(colorReset)
	}

	// Collect all attributes, tracking if there's a "payload" for TRACE
	var payloadValue string
	hasPayload := false

	attrs := make([]slog.Attr, 0, r.NumAttrs()+len(h.attrs))
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "payload" && r.Level == LevelTrace {
			hasPayload = true
			payloadValue = fmt.Sprintf("%v", a.Value.Any())
		} else {
			attrs = append(attrs, a)
		}
		return true
	})
	attrs = append(attrs, h.attrs...)

	// Remaining attributes on the same line
	for _, a := range attrs {
		sb.WriteString(" ")
		sb.WriteString(formatCompactAttr(a, h.groups, h.colors))
	}

	sb.WriteString("\n")

	// Special case: TRACE with payload on separate indented line
	if hasPayload {
		lines := strings.Split(payloadValue, "\n")
		for _, line := range lines {
			sb.WriteString("\t")
			if h.colors {
				sb.WriteString(colorGray)
			}
			sb.WriteString(line)
			if h.colors {
				sb.WriteString(colorReset)
			}
			sb.WriteString("\n")
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write([]byte(sb.String()))
	return err
}

// WithAttrs implements slog.Handler.
func (h *CompactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &CompactHandler{
		w:      h.w,
		opts:   h.opts,
		colors: h.colors,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

// WithGroup implements slog.Handler.
func (h *CompactHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups), len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups = append(newGroups, name)
	return &CompactHandler{
		w:      h.w,
		opts:   h.opts,
		colors: h.colors,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

func levelColor(level slog.Level) string {
	switch level {
	case slog.LevelInfo:
		return colorDarkGreen
	case slog.LevelWarn:
		return colorYellow
	case slog.LevelError:
		return colorRed
	case LevelTrace:
		return colorGray
	case slog.LevelDebug:
		return colorBlue
	default:
		return colorBrightWhite
	}
}

func formatCompactAttr(a slog.Attr, groups []string, colors bool) string {
	if a.Value.Kind() == slog.KindGroup {
		var parts []string
		for _, ga := range a.Value.Group() {
			groupPrefix := append(groups, a.Key)
			parts = append(parts, formatCompactAttr(ga, groupPrefix, colors))
		}
		return strings.Join(parts, " ")
	}

	key := a.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	value := fmt.Sprintf("%v", a.Value.Any())
	if colors {
		return colorCyan + key + colorReset + "=" + value
	}
	return fmt.Sprintf("%s=%v", key, a.Value.Any())
}
