// Package output is the nise CLI's only writer boundary: every byte a
// command prints goes out through a Writer built by this package, never
// through fmt.Println or a raw io.Writer held by command code directly.
//
// The central guarantee this package exists to provide is structural, not
// conventional: in JSON mode, ANSI escape codes cannot reach stdout, no
// matter what a command passes in. That is enforced two ways. First,
// jsonWriter's Line/Success/Verbosef/Banner/Spinner methods — the
// prose-oriented, human-only vocabulary — are no-ops; only Result and Error
// write anything in JSON mode, and both write exactly one JSON document per
// call. Second, and this is the belt-and-suspenders part a regression test
// checks (see json_test.go's TestJSONWriterNeverEmitsANSI and
// json_source_test.go's source scan), every string value in a document
// jsonWriter writes is sanitized — every ESC (0x1b) rune removed from its
// actual content — before that document is JSON-encoded, not scrubbed from
// the encoded bytes afterward. That distinction matters: encoding/json
// escapes a raw ESC byte inside a string into the six-character textual
// sequence "\u001b" before the byte ever reaches the wire, so a post-encode
// byte scan (an earlier version of this package's approach, before review
// caught it) finds nothing to remove — the wire bytes never contained a
// raw 0x1b byte in the first place — while the escaped text still
// round-trips right back into a real ESC byte for any consumer that
// actually parses the JSON, such as `nise --json ... | jq -r .field`.
// Sanitizing the string's content before it is ever encoded means that
// escape sequence is never produced to begin with. A future command that
// accidentally hands a pre-colored string to Result cannot leak ANSI
// through this writer even if every other safeguard is bypassed.
//
// Color, animation, quiet, and verbose are resolved once by the caller
// (see internal/cli's flag and internal/cli/term's capability handling)
// into an Options value and passed to New explicitly. Nothing in this
// package reads an environment variable or a global.
package output
