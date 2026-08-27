package dev

import (
	"strings"
	"sync"
	"testing"
)

func TestPrefixWriterTagsWholeLinesOnly(t *testing.T) {
	t.Parallel()
	var got []string
	w := NewPrefixWriter("app", false, func(s string) { got = append(got, s) })

	// A child's write boundaries are arbitrary; a single compiler error
	// arriving in three writes must still be one line.
	for _, chunk := range []string{"./internal/app/", "app.go:12:2: ", "undefined: Foo\nnext line\npart"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	want := []string{"[app] ./internal/app/app.go:12:2: undefined: Foo", "[app] next line"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lines = %q, want %q (the partial line must still be buffered)", got, want)
	}

	// The trailing partial line only appears on Flush, which is exactly
	// how a crash message with no final newline reaches the terminal.
	w.Flush()
	if len(got) != 3 || got[2] != "[app] part" {
		t.Fatalf("after Flush lines = %q, want the buffered partial line", got)
	}
	w.Flush()
	if len(got) != 3 {
		t.Fatalf("a second Flush emitted %q; it must be a no-op", got[3:])
	}
}

func TestPrefixWriterTrimsCarriageReturns(t *testing.T) {
	t.Parallel()
	var got []string
	w := NewPrefixWriter("web", false, func(s string) { got = append(got, s) })
	if _, err := w.Write([]byte("windows line\r\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got) != 1 || got[0] != "[web] windows line" {
		t.Fatalf("lines = %q, want the carriage return trimmed", got)
	}
}

func TestPrefixWriterBoundsALineThatNeverEnds(t *testing.T) {
	t.Parallel()
	var got []string
	w := NewPrefixWriter("web", false, func(s string) { got = append(got, s) })
	if _, err := w.Write([]byte(strings.Repeat("x", maxBufferedLine+1))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a %d-byte write with no newline produced %d lines, want 1 flushed line", maxBufferedLine+1, len(got))
	}
}

func TestPrefixWriterIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var got []string
	w := NewPrefixWriter("app", false, func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	})

	// os/exec copies a child's stdout and stderr from two goroutines, and
	// both may share this writer.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = w.Write([]byte("line\n"))
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 400 {
		t.Fatalf("got %d lines, want 400", len(got))
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "no escapes here", "no escapes here"},
		{"color", "\x1b[32mready\x1b[0m in 214 ms", "ready in 214 ms"},
		{"bold and reset", "\x1b[1m\x1b[4mVITE\x1b[22m\x1b[24m v7", "VITE v7"},
		{"cursor movement", "a\x1b[2Kb\x1b[1Ac", "abc"},
		{"osc hyperlink bel", "see \x1b]8;;http://localhost\x07here\x1b]8;;\x07", "see here"},
		{"osc hyperlink st", "see \x1b]8;;http://x\x1b\\here", "see here"},
		// ESC followed by a single final byte is a complete two-character
		// escape sequence under ECMA-48, so both bytes go.
		{"two-character escape", "a\x1bbc", "ac"},
		{"trailing escape", "done\x1b", "done"},
		{"unterminated csi", "x\x1b[38;5;", "x"},
		{"multibyte survives", "\x1b[31mcafé — ready\x1b[0m", "café — ready"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := StripANSI(tc.in)
			if got != tc.want {
				t.Fatalf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsRune(got, 0x1b) {
				t.Fatalf("StripANSI(%q) = %q, which still carries an escape byte", tc.in, got)
			}
		})
	}
}

func TestPrefixWriterStripsANSIWhenColorIsOff(t *testing.T) {
	t.Parallel()
	var withColor, withoutColor []string
	on := NewPrefixWriter("web", false, func(s string) { withColor = append(withColor, s) })
	off := NewPrefixWriter("web", true, func(s string) { withoutColor = append(withoutColor, s) })
	const line = "\x1b[32mVITE ready\x1b[0m\n"
	for _, w := range []*PrefixWriter{on, off} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if !strings.ContainsRune(withColor[0], 0x1b) {
		t.Fatalf("with color enabled the child's own styling must survive, got %q", withColor[0])
	}
	if withoutColor[0] != "[web] VITE ready" {
		t.Fatalf("with color disabled, got %q, want the styling removed", withoutColor[0])
	}
}
