package logging

import (
	"strings"
	"testing"
)

func TestNewID_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewID()
		if len(id) != 22 {
			t.Fatalf("NewID() = %q, length %d, want 22", id, len(id))
		}
		if !ValidID(id) {
			t.Fatalf("NewID() = %q is not ValidID", id)
		}
		if strings.ContainsAny(id, "=/+") {
			t.Fatalf("NewID() = %q contains a padding or non-URL-safe base64 character", id)
		}
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}

// TestValidID_HostileInputs exercises exactly the hostile-input categories
// the brief names: newline injection (log forging), ANSI escape sequences
// (terminal injection), an oversized value, the empty string, and non-ASCII
// input. Every one of them must be rejected.
func TestValidID_HostileInputs(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"empty string", "", false},
		{"newline injection", "abc\ninjected-log-line: fake event", false},
		{"carriage return injection", "abc\r\ninjected", false},
		{"ansi escape sequence", "abc\x1b[31mRED\x1b[0m", false},
		{"ansi escape terminal title", "abc\x1b]0;pwned\x07", false},
		{"10KB value", strings.Repeat("a", 10*1024), false},
		{"exactly at max length", strings.Repeat("a", MaxIDLength), true},
		{"one over max length", strings.Repeat("a", MaxIDLength+1), false},
		{"non-ascii", "café-résumé-🎉", false},
		{"null byte", "abc\x00def", false},
		{"embedded space", "abc def", false},
		{"embedded quote", `abc"def`, false},
		{"embedded backtick", "abc`whoami`", false},
		{"path traversal shaped", "../../etc/passwd", false},
		{"valid hyphen and underscore", "req-abc_123", true},
		{"valid uppercase and digits", "ABC123xyz", true},
		{"a generated id", NewID(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidID(tt.id); got != tt.want {
				t.Errorf("ValidID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
