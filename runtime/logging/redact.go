package logging

import (
	"context"
	"log/slog"
	"net/url"
	"sort"
	"strings"
)

// RedactPlaceholder is the default replacement value NewRedactingHandler and
// Redact substitute for a value they redact.
const RedactPlaceholder = "[REDACTED]"

// defaultDenyKeys is the built-in deny set. Comparison against it is always
// case-insensitive and exact: this list does not do substring or prefix
// matching. An application whose field names do not literally match one of
// these (for example "access_token" rather than "token") must add it via
// RedactOptions.ExtraDenyKeys.
var defaultDenyKeys = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"cookie",
	"set-cookie",
	"api_key",
	"apikey",
	"session",
	"csrf",
	"private_key",
	"credential",
}

// DefaultDenyKeys returns the built-in redaction deny set that
// NewRedactingHandler applies when RedactOptions.ExtraDenyKeys is empty. The
// returned slice is a sorted copy; mutating it has no effect on the package
// default.
func DefaultDenyKeys() []string {
	out := make([]string, len(defaultDenyKeys))
	copy(out, defaultDenyKeys)
	sort.Strings(out)
	return out
}

// RedactOptions configures NewRedactingHandler.
type RedactOptions struct {
	// ExtraDenyKeys are attribute key names, matched case-insensitively and
	// exactly, redacted in addition to DefaultDenyKeys.
	ExtraDenyKeys []string

	// Placeholder replaces the value of a redacted attribute. The zero value
	// uses RedactPlaceholder.
	Placeholder string
}

// NewRedactingHandler wraps next so that any attribute whose key matches the
// deny set — case-insensitive, exact match, DefaultDenyKeys plus
// opts.ExtraDenyKeys — has its value replaced with the placeholder before it
// reaches next.
//
// Redaction is applied on every WithAttrs and Handle call, so it survives an
// attribute nested arbitrarily deep under WithGroup or slog.Group: this
// handler resolves each attribute's value (following any slog.LogValuer
// chain via slog.Value.Resolve) and, when the resolved value is itself a
// group, recurses into its members before deciding whether to redact them.
// A slog.LogValuer whose returned value is a group containing a
// deny-set-shaped key is therefore still redacted even though the outer
// attribute's own key is not deny-listed.
//
// Redaction is key-based and cannot inspect a message string; see the
// package doc comment and Redact for that limitation and its mitigation for
// connection strings and URLs.
func NewRedactingHandler(next slog.Handler, opts RedactOptions) slog.Handler {
	deny := make(map[string]struct{}, len(defaultDenyKeys)+len(opts.ExtraDenyKeys))
	for _, k := range defaultDenyKeys {
		deny[strings.ToLower(k)] = struct{}{}
	}
	for _, k := range opts.ExtraDenyKeys {
		deny[strings.ToLower(k)] = struct{}{}
	}
	placeholder := opts.Placeholder
	if placeholder == "" {
		placeholder = RedactPlaceholder
	}
	return &redactingHandler{next: next, deny: deny, placeholder: placeholder}
}

type redactingHandler struct {
	next        slog.Handler
	deny        map[string]struct{}
	placeholder string
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, nr)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redactAttr(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted), deny: h.deny, placeholder: h.placeholder}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name), deny: h.deny, placeholder: h.placeholder}
}

func (h *redactingHandler) isDenied(key string) bool {
	_, ok := h.deny[strings.ToLower(key)]
	return ok
}

// redactAttr resolves a's value (following any slog.LogValuer chain) and
// redacts it wholesale if a's own key is deny-listed, or, when the resolved
// value is a group, recurses into each member so a deny-listed key nested at
// any depth is still caught.
func (h *redactingHandler) redactAttr(a slog.Attr) slog.Attr {
	if h.isDenied(a.Key) {
		return slog.String(a.Key, h.placeholder)
	}
	v := a.Value.Resolve()
	if v.Kind() != slog.KindGroup {
		return slog.Attr{Key: a.Key, Value: v}
	}
	group := v.Group()
	redacted := make([]slog.Attr, len(group))
	for i, ga := range group {
		redacted[i] = h.redactAttr(ga)
	}
	return slog.Attr{Key: a.Key, Value: slog.GroupValue(redacted...)}
}

// Redact returns a copy of raw, an absolute URL such as a PostgreSQL or SMTP
// connection URI, with userinfo (both username and password) removed
// entirely and any query parameter whose name is in DefaultDenyKeys replaced
// with RedactPlaceholder. The result is safe to place in a log message or
// error string.
//
// Redact only recognizes deny-set-shaped query parameter names; a
// credential passed under a different parameter name is not removed. It has
// no way to parse a connection string that is not URL-shaped, such as
// PostgreSQL's space-separated "key=value" DSN syntax. When raw does not
// parse as an absolute URL with both a scheme and a host, Redact cannot
// verify it contains no secret and fails closed: it returns a fixed
// placeholder rather than risk returning raw, potentially secret-bearing,
// text unredacted.
func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "[REDACTED: unparsable connection string]"
	}

	redacted := *u
	redacted.User = nil

	if redacted.RawQuery != "" {
		q := redacted.Query()
		for key := range q {
			if isDefaultDeniedKey(key) {
				q.Set(key, RedactPlaceholder)
			}
		}
		redacted.RawQuery = q.Encode()
	}

	return redacted.String()
}

func isDefaultDeniedKey(key string) bool {
	lower := strings.ToLower(key)
	for _, k := range defaultDenyKeys {
		if k == lower {
			return true
		}
	}
	return false
}
