package output

import (
	"bytes"
	"encoding/json"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// jsonWriter renders exactly one JSON document per Result or Error call, on
// stdout, and nothing else. This file must never contain an ANSI escape
// literal — json_source_test.go greps this exact file for one and fails the
// build's test suite if it finds one — and every document this type writes
// passes through stripANSI as a second, independent line of defense (see
// stripANSI below and TestJSONWriterNeverEmitsANSI in json_test.go).
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

// writeDoc encodes v and writes it as one line to stdout, after stripping
// any ESC byte from the encoded bytes. HTML-escaping is disabled: nise's
// JSON output is read by scripts and jq, not embedded in an HTML page, so
// "<", ">", and "&" should read as themselves rather than as \uXXXX
// escapes. Encode failure itself becomes a (still ANSI-free) JSON error
// document rather than a panic or a silently empty line, since a command
// author's Result type failing to marshal is a bug this writer should
// surface, not hide.
func (w *jsonWriter) writeDoc(v any) {
	b := encodeJSONLine(v)
	if b == nil {
		b = encodeJSONLine(map[string]string{
			"error": "internal: failed to encode JSON output",
		})
	}
	w.stdout.writeString(string(stripANSI(b)))
}

// encodeJSONLine returns v encoded as a single JSON document followed by
// exactly one newline (json.Encoder.Encode's own behavior), or nil if v
// could not be encoded — the only case writeDoc needs to distinguish.
func encodeJSONLine(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return buf.Bytes()
}

// stripANSI returns b with every ESC (0x1b) byte removed. This is the
// structural backstop for the "--json never emits ANSI" guarantee: even if
// every other safeguard (no color helper exists in this file; Line/Success/
// Banner are no-ops; clierr.Error never stores pre-colored text) were
// somehow bypassed by a future change, a byte that made it into v's
// marshaled JSON would still never reach stdout with its escape byte
// intact.
func stripANSI(b []byte) []byte {
	hasEsc := false
	for _, c := range b {
		if c == 0x1b {
			hasEsc = true
			break
		}
	}
	if !hasEsc {
		return b
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0x1b {
			continue
		}
		out = append(out, c)
	}
	return out
}
