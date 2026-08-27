package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// styledResult simulates a future command mistakenly handing a
// pre-ANSI-colored string to Result. jsonWriter must still emit no ANSI
// escape, in any form: this is the regression TestJSONWriterNeverEmitsANSI
// exists to catch, independent of json_source_test.go's static source
// scan.
type styledResult struct {
	Label string `json:"label"`
}

func (s styledResult) Human() string { return s.Label }

// jsonOutputHasANSI reports whether b — raw JSON bytes as written by
// jsonWriter — could reconstruct an ANSI escape byte once decoded by any
// JSON-aware consumer (jq, a script's own json.Unmarshal, ...).
//
// A raw ESC (0x1b) byte never actually appears in valid JSON text —
// encoding/json always escapes a control character inside a string into a
// six-character textual sequence before it reaches the wire — so checking
// only for the raw byte (as this package's tests originally did) proves
// nothing about whether the string's real content is ANSI-free: it always
// passes, encoded or not, which is exactly the gap review found in the
// pre-fix stripANSI. The meaningful check is the textual escape sequence
// itself, since that is what a JSON decoder turns back into a real ESC
// byte for whoever reads the field.
func jsonOutputHasANSI(b []byte) bool {
	if bytes.ContainsRune(b, 0x1b) {
		return true
	}
	lower := bytes.ToLower(b)
	needle := []byte{'\\', 'u', '0', '0', '1', 'b'}
	return bytes.Contains(lower, needle)
}

func TestJSONWriterResultParsesAsJSONWithNoANSI(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	w.Result(fixtureResult{Name: "doctor"})

	out := buf.Bytes()
	if jsonOutputHasANSI(out) {
		t.Fatalf("Result output contains an ANSI escape: %q", out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output did not parse as JSON: %v\noutput: %s", err, out)
	}
	if got["name"] != "doctor" {
		t.Errorf("decoded name = %v, want doctor", got["name"])
	}
}

func TestJSONWriterErrorParsesAsJSONWithNoANSI(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	ce := clierr.Usage("unknown command \"fro\"", "Run \"nise help\".")
	w.Error(ce)

	out := buf.Bytes()
	if jsonOutputHasANSI(out) {
		t.Fatalf("Error output contains an ANSI escape: %q", out)
	}
	var env clierr.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output did not parse as the error envelope: %v\noutput: %s", err, out)
	}
	if env.Error.Code != "usage_error" {
		t.Errorf("error.code = %q, want usage_error", env.Error.Code)
	}
	if env.Error.Message != `unknown command "fro"` {
		t.Errorf("error.message = %q", env.Error.Message)
	}
}

// TestJSONWriterNeverEmitsANSI is the regression test the brief asks for:
// even a Result value whose string field already contains a raw ANSI
// escape sequence (simulating a future command that reused a human-styled
// string in JSON mode) must not reach stdout in any form that would
// reconstruct into a real ESC byte for a consumer decoding the JSON.
// Checking only for a raw 0x1b byte in the wire bytes is not enough,
// because encoding/json always escapes one into a six-character textual
// sequence, which round-trips right back into a real ESC byte the moment
// anything actually parses the JSON — jsonOutputHasANSI checks both forms
// for exactly this reason. Review verified this test actually catches a
// regression by short-circuiting the sanitization step and re-running it,
// which then failed as expected before this fix.
func TestJSONWriterNeverEmitsANSI(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	styled := "\x1b[32mdone\x1b[0m"
	w.Result(styledResult{Label: styled})

	out := buf.Bytes()
	if jsonOutputHasANSI(out) {
		t.Fatalf("sanitizeANSI failed to remove an ESC rune from Result output: %q", out)
	}
	if !bytes.Contains(out, []byte("done")) {
		t.Fatalf("sanitized output lost the surrounding text: %q", out)
	}
}

// TestJSONWriterNeverEmitsANSINested proves sanitizeANSI reaches strings
// nested inside maps and slices, not just a Result's top-level fields —
// the shape a future command's structured output (e.g. doctor's per-tool
// check list) will actually take.
func TestJSONWriterNeverEmitsANSINested(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	styled := nestedStyledResult{
		Items: []nestedItem{
			{Name: "one", Note: "\x1b[32mok\x1b[0m"},
			{Name: "two", Note: "plain"},
		},
	}
	w.Result(styled)

	out := buf.Bytes()
	if jsonOutputHasANSI(out) {
		t.Fatalf("sanitizeANSI missed a nested ESC rune: %q", out)
	}
	if !bytes.Contains(out, []byte("ok")) {
		t.Fatalf("sanitized output lost nested surrounding text: %q", out)
	}
}

type nestedItem struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

type nestedStyledResult struct {
	Items []nestedItem `json:"items"`
}

func (n nestedStyledResult) Human() string { return "" }

func TestSanitizeANSI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"plain string", "hello", "hello"},
		{"string with ESC", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"bool passes through", true, true},
		{"nil passes through", nil, nil},
		{
			name: "nested map and slice",
			in: map[string]any{
				"a": "\x1b[1mbold\x1b[0m",
				"b": []any{"\x1b[2mdim\x1b[0m", "clean"},
			},
			want: map[string]any{
				"a": "[1mbold[0m",
				"b": []any{"[2mdim[0m", "clean"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeANSI(tt.in)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("sanitizeANSI(%#v) = %s, want %s", tt.in, gotJSON, wantJSON)
			}
		})
	}
}

func TestStripANSIRunes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no escape runes", "hello", "hello"},
		{"escape rune removed", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripANSIRunes(tt.in)
			if got != tt.want {
				t.Errorf("stripANSIRunes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEncodeSanitizedPreservesLargeIntegers proves the marshal → decode
// (UseNumber) → sanitize → marshal round trip in encodeSanitized does not
// lose precision on an integer bigger than float64 can represent exactly
// — the risk any round-trip-through-interface{} approach normally carries,
// avoided here specifically by decoding with UseNumber rather than into a
// plain float64.
func TestEncodeSanitizedPreservesLargeIntegers(t *testing.T) {
	t.Parallel()
	type bigNum struct {
		N int64 `json:"n"`
	}
	// 2^53 + 1: the smallest positive integer a float64 cannot represent
	// exactly.
	const want = 9007199254740993
	b := encodeSanitized(bigNum{N: want})

	var got bigNum
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", b, err)
	}
	if got.N != want {
		t.Errorf("N = %d, want %d (precision lost in the sanitize round trip)", got.N, want)
	}
}

func TestJSONWriterOneDocumentPerLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	w.Result(fixtureResult{Name: "a"})
	w.Result(fixtureResult{Name: "b"})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var got fixtureResult
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d did not parse as JSON on its own: %v (%q)", i, err, line)
		}
	}
}

func TestJSONWriterProseMethodsAreNoOps(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{Verbose: true}, &buf, &bytes.Buffer{})

	w.Line("hello")
	w.Verbosef("detail %d", 1)
	w.Success("done")
	w.Banner("nise")
	w.Spinner("working").Stop("finished")

	if buf.Len() != 0 {
		t.Errorf("JSON mode prose methods wrote output: %q", buf.String())
	}
}

func TestJSONWriterErrorChainOnlyWithVerbose(t *testing.T) {
	t.Parallel()

	underlying := errors.New("dial tcp: refused")
	ce := clierr.Wrap(underlying, clierr.ExitPrecondition, "could not reach the database", "start postgres and retry")

	var quiet bytes.Buffer
	New(ModeJSON, Options{Verbose: false}, &quiet, &bytes.Buffer{}).Error(ce)
	var quietEnv clierr.Envelope
	if err := json.Unmarshal(quiet.Bytes(), &quietEnv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if quietEnv.Error.Chain != nil {
		t.Errorf("verbose=false: chain = %v, want omitted", quietEnv.Error.Chain)
	}

	var verbose bytes.Buffer
	New(ModeJSON, Options{Verbose: true}, &verbose, &bytes.Buffer{}).Error(ce)
	var verboseEnv clierr.Envelope
	if err := json.Unmarshal(verbose.Bytes(), &verboseEnv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(verboseEnv.Error.Chain) != 1 {
		t.Errorf("verbose=true: chain = %v, want 1 line", verboseEnv.Error.Chain)
	}
}

func TestJSONWriterMode(t *testing.T) {
	t.Parallel()
	w := New(ModeJSON, Options{}, &bytes.Buffer{}, &bytes.Buffer{})
	if w.Mode() != ModeJSON {
		t.Errorf("Mode() = %v, want ModeJSON", w.Mode())
	}
}
