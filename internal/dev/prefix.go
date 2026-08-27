package dev

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
)

// maxBufferedLine bounds how much a PrefixWriter holds for a line that
// never ends. A child that writes a hundred megabytes with no newline —
// a progress bar redrawing with carriage returns, or a corrupted stream —
// must not grow nise's heap without limit, so the buffer is flushed as its
// own line once it passes this size.
const maxBufferedLine = 64 << 10

// PrefixWriter turns a child process's byte stream into whole lines, each
// tagged with the source that produced it, and hands them to emit.
//
// Interleaving is why it exists: three children write to one terminal at
// once, and without a per-source tag the result is unreadable. Line
// buffering is why it is not simply an io.MultiWriter — a child's partial
// write must not become a line of its own, or a single Go compiler error
// arrives split across three tagged lines.
//
// It is safe for concurrent use: a child's stdout and stderr are copied by
// two different goroutines inside os/exec, and both may share one writer.
type PrefixWriter struct {
	prefix string
	emit   func(string)
	strip  bool

	mu  sync.Mutex
	buf []byte
}

// NewPrefixWriter returns a writer that tags every line from source and
// passes it to emit.
//
// When stripANSI is set, escape sequences are removed from the child's
// output. That is how `nise dev` honors a request for uncolored output
// without asking every child to cooperate: Vite colors its output whenever
// it believes it is talking to a terminal, and it is talking to a pipe held
// by nise, not to the terminal the developer is reading.
func NewPrefixWriter(source string, stripANSI bool, emit func(string)) *PrefixWriter {
	return &PrefixWriter{prefix: "[" + source + "] ", emit: emit, strip: stripANSI}
}

// Write implements io.Writer.
func (w *PrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxBufferedLine {
		w.line(w.buf)
		w.buf = nil
	}
	return len(p), nil
}

// Flush emits any buffered partial line. Call it once after the child
// exits: os/exec's copy goroutines stop at EOF without telling this writer
// the stream ended, so a final line with no trailing newline — which is
// exactly how a crash message often arrives — would otherwise be lost.
func (w *PrefixWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.line(w.buf)
		w.buf = nil
	}
}

// line emits one line. The caller holds the lock.
func (w *PrefixWriter) line(b []byte) {
	s := strings.TrimSuffix(string(b), "\r")
	if w.strip {
		s = StripANSI(s)
	}
	w.emit(w.prefix + s)
}

// StripANSI removes ANSI escape sequences from s.
//
// It recognizes the two forms a build tool actually emits: CSI sequences
// (ESC [ … final-byte), which carry color and cursor movement, and OSC
// sequences (ESC ] … BEL or ESC \), which carry hyperlinks and window
// titles. Anything else beginning with ESC has its ESC dropped, so no raw
// escape byte survives into output nise is responsible for.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		i++ // consume ESC
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: parameters, then a final byte in @..~
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++
			}
		case ']': // OSC: terminated by BEL or ESC \
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			i++ // a two-character escape; drop both
		}
	}
	return b.String()
}
