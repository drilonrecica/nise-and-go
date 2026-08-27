package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
)

// TextOptions configures NewTextHandler.
type TextOptions struct {
	HandlerOptions

	// Color wraps the level field in ANSI colour codes when true. This
	// package never inspects the writer or the environment to decide this
	// itself — terminal detection belongs to the CLI, which knows whether
	// its output is actually a terminal and whether the user disabled
	// colour. The caller always passes an explicit value.
	Color bool
}

// NewTextHandler returns a slog.Handler that writes one concise,
// human-readable line per record to w, intended for local development: a
// millisecond-precision time, the level aligned to a fixed width, the
// message, then attributes in the order they were added — "key=value",
// quoted with strconv.Quote when a value needs it. Attribute order is always
// the order of addition, across chained slog.Logger.With and WithGroup calls
// and the attributes passed to the logging call itself; this handler never
// uses map iteration to decide output order, so its output is deterministic
// and safe for tests to assert on exactly.
//
// As with NewJSONHandler, every attribute passes through central redaction
// before it reaches this handler's formatting.
func NewTextHandler(w io.Writer, opts TextOptions) slog.Handler {
	level := opts.Level
	if level == nil {
		level = slog.LevelInfo
	}
	base := &textHandler{
		mu:    &sync.Mutex{},
		w:     w,
		level: level,
		color: opts.Color,
	}
	return NewRedactingHandler(base, opts.Redact)
}

// textHandler implements slog.Handler directly. Central redaction is applied
// by the wrapper NewTextHandler returns, not here: this type only formats.
type textHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	color bool

	// prefix is the dot-joined name of every enclosing WithGroup call, with a
	// trailing dot when non-empty (e.g. "request." or "request.http.").
	prefix string

	// attrs holds attributes accumulated through WithAttrs, with prefix
	// already applied to each key at the time it was added. Storing them
	// pre-prefixed, rather than re-deriving the prefix at Handle time, keeps
	// each group level's prefix applied exactly once.
	attrs []slog.Attr
}

func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer
	buf.WriteString(r.Time.Format("15:04:05.000"))
	buf.WriteByte(' ')
	buf.WriteString(h.formatLevel(r.Level))
	buf.WriteByte(' ')
	buf.WriteString(r.Message)

	for _, a := range h.attrs {
		writeAttr(&buf, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&buf, h.prefix, a)
		return true
	})
	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	added := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		added[i] = slog.Attr{Key: h.prefix + a.Key, Value: a.Value}
	}
	merged := make([]slog.Attr, 0, len(h.attrs)+len(added))
	merged = append(merged, h.attrs...)
	merged = append(merged, added...)
	return &textHandler{mu: h.mu, w: h.w, level: h.level, color: h.color, prefix: h.prefix, attrs: merged}
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &textHandler{mu: h.mu, w: h.w, level: h.level, color: h.color, prefix: h.prefix + name + ".", attrs: h.attrs}
}

// formatLevel renders level as a fixed-width, aligned label, optionally
// wrapped in an ANSI colour code.
func (h *textHandler) formatLevel(level slog.Level) string {
	label, code := levelLabel(level)
	if !h.color {
		return label
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, label)
}

// levelLabel returns a fixed-width (5-rune) label for level and the ANSI SGR
// code used to colour it: 2 (faint) for debug, 36 (cyan) for info, 33
// (yellow) for warn, 31 (red) for error and above.
func levelLabel(level slog.Level) (label string, ansiCode string) {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG", "2"
	case level < slog.LevelWarn:
		return "INFO ", "36"
	case level < slog.LevelError:
		return "WARN ", "33"
	default:
		return "ERROR", "31"
	}
}

// writeAttr appends " key=value" to buf for a, applying prefix to its key.
// When a's resolved value is itself a group, writeAttr recurses into the
// group's members instead of writing a value for a, using key+"." as the
// prefix for those members.
func writeAttr(buf *bytes.Buffer, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	key := prefix + a.Key

	if v.Kind() == slog.KindGroup {
		groupPrefix := key
		if groupPrefix != "" {
			groupPrefix += "."
		}
		for _, ga := range v.Group() {
			writeAttr(buf, groupPrefix, ga)
		}
		return
	}

	buf.WriteByte(' ')
	buf.WriteString(key)
	buf.WriteByte('=')
	buf.WriteString(formatValue(v))
}

// formatValue renders v as it should appear after "key=", quoting with
// strconv.Quote when the unquoted form would be ambiguous or unreadable.
func formatValue(v slog.Value) string {
	s := v.String()
	if needsQuote(s) {
		return strconv.Quote(s)
	}
	return s
}

// needsQuote reports whether s must be quoted to appear unambiguously after
// "key=" on a single space-delimited log line.
func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '"' || c == '=' || c == '\\' {
			return true
		}
	}
	return false
}
