package logging

import (
	"context"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// RedactPlaceholder is the default replacement value newRedactingHandler and
// Redact substitute for a value they redact.
const RedactPlaceholder = "[REDACTED]"

// defaultDenyFragments is the built-in deny set. Each entry is a normalized
// fragment, not a literal key: a key is redacted when its normalized form
// (see normalizeKeySegments) contains the fragment, not only when it equals
// one exactly. This is what lets the single fragment "token" catch
// "access_token", "refresh_token", and "Auth-Token" alike, and the single
// fragment "apikey" catch "api_key", "api-key", and "APIKey".
//
// "session" is narrower than plain containment (see isDeniedKey): a bare
// containment match on "session" would also redact operator-relevant metrics
// like session_count and session_start_time, which is a worse outcome than
// missing an edge case. See docs/observability.md for the full list of
// accepted false positives this trade-off produces for the other fragments.
var defaultDenyFragments = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"cookie",
	"apikey",
	"session",
	"csrf",
	"privatekey",
	"credential",
}

// boundedFragments are default deny fragments broad enough on their own to
// match common, non-secret operational field names. A fragment in this set
// matches only when the normalized key is exactly the fragment on its own,
// or when the fragment appears together with "id", "key", or "token" as a
// separate normalized word — for example "session" alone, "sessionID", and
// "session_token" all match, but "session_count" and "session_start_time" do
// not. Every other fragment, default or added through
// RedactOptions.ExtraDenyKeys, matches by plain containment instead; see
// isDeniedKey.
var boundedFragments = map[string]struct{}{
	"session": {},
}

// DefaultDenyKeys returns the built-in redaction deny set — normalized
// fragments, not literal key names; see isDeniedKey — that every handler
// this package constructs applies when RedactOptions.ExtraDenyKeys is empty.
// The returned slice is a sorted copy; mutating it has no effect on the
// package default.
func DefaultDenyKeys() []string {
	out := make([]string, len(defaultDenyFragments))
	copy(out, defaultDenyFragments)
	sort.Strings(out)
	return out
}

// IsSensitiveKey reports whether key names something that must not be recorded
// in the clear, under the same rule this package's handlers apply.
//
// It is exported so a second store of structured data — an audit record, say —
// can refuse a field the logger would have redacted, using this rule rather
// than a copy of it. Two deny lists in one codebase drift, and the one that
// drifts is discovered by whatever it failed to redact.
//
// extra adds fragments beyond [DefaultDenyKeys], matched the same way.
func IsSensitiveKey(key string, extra ...string) bool {
	fragments := defaultDenyFragments
	if len(extra) > 0 {
		fragments = append(append([]string(nil), defaultDenyFragments...), extra...)
	}
	return isDeniedKey(key, fragments)
}

// RedactOptions configures newRedactingHandler and Redact's query-parameter
// matching.
type RedactOptions struct {
	// ExtraDenyKeys are additional deny-set fragments, matched the same way
	// as DefaultDenyKeys — case-folded, separator- and camelCase-normalized,
	// plain containment (never narrowed the way "session" is; see
	// boundedFragments) — in addition to DefaultDenyKeys. Prefer a specific
	// fragment ("db_password" normalizes the same as "password" already
	// does, so it needs no entry) over a generic one, since a generic extra
	// fragment gets none of the narrowing this package applies to its own
	// broad default fragments.
	ExtraDenyKeys []string

	// Placeholder replaces the value of a redacted attribute. The zero value
	// uses RedactPlaceholder.
	Placeholder string
}

// newRedactingHandler wraps next so that any attribute whose key matches the
// deny set — DefaultDenyKeys plus opts.ExtraDenyKeys, see isDeniedKey for
// exactly what "matches" means — has its value replaced with the placeholder
// before it reaches next.
//
// This type is deliberately unexported. NewJSONHandler and NewTextHandler
// already apply it through HandlerOptions.Redact, which is the supported way
// to get redaction from this package; exporting a second, independent way to
// construct the same wrapper would widen the pre-1.0 public contract further
// than the current callers need. RedactOptions, DefaultDenyKeys, and Redact
// remain exported because applications genuinely need to configure and reuse
// them.
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
func newRedactingHandler(next slog.Handler, opts RedactOptions) slog.Handler {
	fragments := make([]string, 0, len(defaultDenyFragments)+len(opts.ExtraDenyKeys))
	fragments = append(fragments, defaultDenyFragments...)
	for _, k := range opts.ExtraDenyKeys {
		fragments = append(fragments, normalizeKey(k))
	}
	placeholder := opts.Placeholder
	if placeholder == "" {
		placeholder = RedactPlaceholder
	}
	return &redactingHandler{next: next, fragments: fragments, placeholder: placeholder}
}

type redactingHandler struct {
	next        slog.Handler
	fragments   []string
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
	return &redactingHandler{next: h.next.WithAttrs(redacted), fragments: h.fragments, placeholder: h.placeholder}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name), fragments: h.fragments, placeholder: h.placeholder}
}

func (h *redactingHandler) isDenied(key string) bool {
	return isDeniedKey(key, h.fragments)
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

// isDeniedKey reports whether key matches any of fragments once both are
// normalized (see normalizeKeySegments): case-folded, with separators
// removed and camelCase boundaries split. A fragment in boundedFragments
// (currently only "session") matches only when it is the key's only
// normalized word, or when it shares the key with "id", "key", or "token" as
// a separate word; every other fragment matches by plain containment in the
// normalized, separator-free key.
func isDeniedKey(key string, fragments []string) bool {
	words := normalizeKeySegments(key)
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, "")

	for _, fragment := range fragments {
		if _, bounded := boundedFragments[fragment]; bounded {
			if matchesBoundedFragment(words, fragment) {
				return true
			}
			continue
		}
		if strings.Contains(joined, fragment) {
			return true
		}
	}
	return false
}

// matchesBoundedFragment reports whether fragment appears as its own word in
// words, and either it is the only word or another word is "id", "key", or
// "token".
func matchesBoundedFragment(words []string, fragment string) bool {
	found := false
	for _, w := range words {
		if w == fragment {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if len(words) == 1 {
		return true
	}
	for _, w := range words {
		if w == "id" || w == "key" || w == "token" {
			return true
		}
	}
	return false
}

// normalizeKey is normalizeKeySegments joined back into one lowercase,
// separator-free string, for a caller (ExtraDenyKeys) that supplies a
// fragment rather than a full key.
func normalizeKey(key string) string {
	return strings.Join(normalizeKeySegments(key), "")
}

// normalizeKeySegments splits key into lowercase word segments, breaking on
// any run of non-alphanumeric separators (such as '_', '-', '.', or space)
// and on a lowercase-or-digit-to-uppercase camelCase boundary — so
// "sessionID" splits into "session" and "id", matching the shape Go and HTTP
// code actually uses for compound field and header names.
func normalizeKeySegments(key string) []string {
	var words []string
	var current []rune

	flush := func() {
		if len(current) > 0 {
			words = append(words, strings.ToLower(string(current)))
			current = current[:0]
		}
	}

	runes := []rune(key)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(runes[i-1]):
			flush()
			current = append(current, r)
		default:
			current = append(current, r)
		}
	}
	flush()

	return words
}

// Redact returns a copy of raw, an absolute URL such as a PostgreSQL or SMTP
// connection URI, with userinfo (both username and password) removed
// entirely and any query parameter whose name matches DefaultDenyKeys (see
// isDeniedKey) replaced with RedactPlaceholder. The result is safe to place
// in a log message or error string.
//
// Redact only recognizes deny-set-shaped query parameter names; a credential
// passed under a name that does not match any deny fragment is not removed.
// It has no way to parse a connection string that is not URL-shaped, such as
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
			if isDeniedKey(key, defaultDenyFragments) {
				q.Set(key, RedactPlaceholder)
			}
		}
		redacted.RawQuery = q.Encode()
	}

	return redacted.String()
}
