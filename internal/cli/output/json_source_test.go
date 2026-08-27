package output

import (
	"os"
	"strings"
	"testing"
)

// TestJSONWriterSourceContainsNoANSILiteral is a static regression guard
// for the structural "never emit ANSI in JSON mode" guarantee: it reads
// json.go's own source and fails if anyone ever pastes an ANSI escape
// sequence (or the "\x1b" literal that produces one) into the file that
// implements --json rendering. Combined with TestJSONWriterNeverEmitsANSI's
// runtime check, a future change would have to defeat both an independent
// source scan and a runtime byte scan to leak color into JSON output.
func TestJSONWriterSourceContainsNoANSILiteral(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("json.go")
	if err != nil {
		t.Fatalf("could not read json.go: %v", err)
	}
	src := string(b)

	// The real escape byte, if it were ever pasted in directly.
	if strings.ContainsRune(src, 0x1b) {
		t.Error("json.go contains a literal ESC (0x1b) byte")
	}
	// The Go source spelling of that byte, other than inside this test's
	// own doc comment / the stripANSIRunes and sanitizeANSI comparisons
	// that exist specifically to remove it (both written as the "0x1b"
	// numeric literal, never the "\x1b" string/rune escape spelling) —
	// this test only runs against json.go, never against itself.
	if strings.Contains(src, `"\x1b`) || strings.Contains(src, `'\x1b`) {
		t.Error(`json.go contains an "\x1b" (or '\x1b') escape-sequence literal outside of its own byte/rune comparisons`)
	}
}
