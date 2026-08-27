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
// pre-ANSI-colored string to Result. jsonWriter must still emit zero ESC
// bytes: this is the regression TestJSONWriterNeverEmitsANSI exists to
// catch, independent of json_source_test.go's static source scan.
type styledResult struct {
	Label string `json:"label"`
}

func (s styledResult) Human() string { return s.Label }

func TestJSONWriterResultParsesAsJSONWithNoANSI(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	w.Result(fixtureResult{Name: "doctor"})

	out := buf.Bytes()
	if bytes.ContainsRune(out, 0x1b) {
		t.Fatalf("Result output contains an ESC byte: %q", out)
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
	if bytes.ContainsRune(out, 0x1b) {
		t.Fatalf("Error output contains an ESC byte: %q", out)
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
// string in JSON mode) must not reach stdout with that escape byte intact.
// If a future change to writeDoc or stripANSI removes this backstop, this
// test fails.
func TestJSONWriterNeverEmitsANSI(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := New(ModeJSON, Options{}, &buf, &bytes.Buffer{})

	styled := "\x1b[32mdone\x1b[0m"
	w.Result(styledResult{Label: styled})

	out := buf.Bytes()
	if bytes.ContainsRune(out, 0x1b) {
		t.Fatalf("stripANSI failed to remove an ESC byte from Result output: %q", out)
	}
	if !bytes.Contains(out, []byte("done")) {
		t.Fatalf("stripped output lost the surrounding text: %q", out)
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"no escape bytes", []byte(`{"a":1}`), []byte(`{"a":1}`)},
		{"escape byte removed", []byte("\x1b[31mred\x1b[0m"), []byte("[31mred[0m")},
		{"empty input", nil, []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripANSI(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
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
