package output

import (
	"bytes"
	"encoding/json"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// jsonWriter renders exactly one JSON document per Result or Error call, on
// stdout, and nothing else. This file must never contain an ANSI escape
// literal — json_source_test.go greps this exact file for one and fails the
// build's test suite if it finds one — and every string value this type
// writes has any ESC (0x1b) rune removed from its actual content before
// encoding (see sanitizeANSI below and TestJSONWriterNeverEmitsANSI in
// json_test.go), not merely scrubbed from the encoded bytes afterward —
// see sanitizeANSI's doc comment for why that distinction is the whole
// fix.
//
// Line/Verbosef/Success/Banner are no-ops: JSON mode carries no human prose
// at all, by design (see docs/cli-output.md). Spinner returns a handle that
// does nothing until Stop, which likewise emits nothing — a command's
// actual result still reaches the caller through Result.
type jsonWriter struct {
	stdout  *safeWriter
	verbose bool
}

func (w *jsonWriter) Mode() Mode { return ModeJSON }

func (w *jsonWriter) Line(string)             {}
func (w *jsonWriter) Verbosef(string, ...any) {}
func (w *jsonWriter) Success(string)          {}
func (w *jsonWriter) Banner(string)           {}

func (w *jsonWriter) Spinner(string) Spinner { return noopSpinner{} }

// noopSpinner is jsonWriter's Spinner: JSON mode has no room for animation
// or prose, so Stop does nothing. A command's actual outcome still reaches
// the caller through Result, called separately.
type noopSpinner struct{}

func (noopSpinner) Stop(string) {}

func (w *jsonWriter) Result(v Result) {
	w.writeDoc(v)
}

func (w *jsonWriter) Error(err error) {
	ce := clierr.From(err)
	w.writeDoc(ce.JSONEnvelope(w.verbose))
}

// writeDoc encodes v, sanitized, and writes it as one line to stdout.
// HTML-escaping is disabled: nise's JSON output is read by scripts and jq,
// not embedded in an HTML page, so "<", ">", and "&" should read as
// themselves rather than as \uXXXX escapes. Encode failure itself becomes
// a JSON error document rather than a panic or a silently empty line,
// since a command author's Result type failing to marshal is a bug this
// writer should surface, not hide.
func (w *jsonWriter) writeDoc(v any) {
	b := encodeSanitized(v)
	if b == nil {
		b = encodeSanitized(map[string]string{
			"error": "internal: failed to encode JSON output",
		})
	}
	w.stdout.writeString(string(b))
}

// encodeSanitized encodes v as a single JSON document (one line, no HTML
// escaping) with every string value first passed through sanitizeANSI, or
// returns nil if v could not be round-tripped through JSON at all.
//
// This is a marshal → decode (with UseNumber, to avoid float64 precision
// loss on any large integer a future command emits) → sanitize → marshal
// round trip, not a single-pass reflect walk over v's original Go type.
// The round trip is what makes sanitization simple and exhaustively
// correct: after the first decode, every value in the tree is one of
// exactly six concrete kinds (nil, bool, json.Number, string,
// map[string]any, []any), so sanitizeANSI never needs to handle an
// arbitrary caller-defined struct, pointer, or custom MarshalJSON — by the
// time it runs, JSON's own type system has already done that work.
func encodeSanitized(v any) []byte {
	first := encodeJSONLine(v)
	if first == nil {
		return nil
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(first))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil
	}
	return encodeJSONLine(sanitizeANSI(generic))
}

// encodeJSONLine returns v encoded as a single JSON document followed by
// exactly one newline (json.Encoder.Encode's own behavior), or nil if v
// could not be encoded.
func encodeJSONLine(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return buf.Bytes()
}

// sanitizeANSI walks a value decoded from JSON (so v is always nil, bool,
// json.Number, string, map[string]any, or []any — see encodeSanitized) and
// returns an equivalent value with every ESC (0x1b) rune removed from
// every string it contains, at any depth.
//
// This must run BEFORE the sanitized value is re-encoded, not after —
// running a byte-level strip on already-encoded JSON bytes (the previous
// approach) cannot work, because encoding/json escapes a raw ESC byte
// inside a string into the six-byte literal text "\u001b" before the byte
// ever reaches the output; there is no longer a 0x1b byte in the wire
// bytes for a post-encode scan to find, even though a JSON *decoder*
// reading that text back (exactly what `nise --json ... | jq -r .field`
// does) reconstructs the real ESC byte from the escape sequence. Removing
// the rune from the string's actual content, before that string is ever
// encoded, means the escape sequence is never produced in the first
// place — jq (or any other JSON-aware consumer) gets a string that
// genuinely has no ESC byte in it, not just wire bytes that don't spell
// one out literally.
func sanitizeANSI(v any) any {
	switch t := v.(type) {
	case string:
		return stripANSIRunes(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = sanitizeANSI(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitizeANSI(val)
		}
		return out
	default:
		// nil, bool, json.Number: none can contain an ESC rune.
		return v
	}
}

// stripANSIRunes returns s with every ESC (0x1b) rune removed.
func stripANSIRunes(s string) string {
	hasEsc := false
	for _, r := range s {
		if r == 0x1b {
			hasEsc = true
			break
		}
	}
	if !hasEsc {
		return s
	}
	var b []rune
	for _, r := range s {
		if r == 0x1b {
			continue
		}
		b = append(b, r)
	}
	return string(b)
}
