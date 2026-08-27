package output

import (
	"fmt"
	"io"
	"sync"
)

// safeWriter centralizes the "a terminal write error is not this command's
// failure to report" decision in exactly one place, rather than scattering
// an ignored fmt.Fprintln/Fprintf return value (or a //nolint comment) at
// every call site — cmd/nise/main.go's five errcheck findings from Task 10
// are exactly that scattered pattern, and this package structurally cannot
// reproduce them: nothing outside this file calls fmt.Fprint* at all.
//
// A write failure here almost always means the reader went away — a closed
// pipe, a killed `| head` — which no nise command can usefully recover from
// or meaningfully report as its own failure. The first error is recorded
// and every later write on that stream is silently skipped, matching how a
// shell pipeline treats SIGPIPE: the producer stops, quietly, once nothing
// is listening.
//
// safeWriter is also this package's synchronization boundary: a live
// spinner's animation goroutine and the command's own calling goroutine
// both write through the same humanWriter concurrently (the natural
// pattern is a spinner running while the command's own Line/Verbosef calls
// continue), so every write takes mu for its whole duration — one
// WriteString call per writeString invocation, never split across the
// lock — which is also why writeLine builds "str\n" as a single string
// before writing it, rather than issuing two separate writes that could
// interleave with a concurrent writer's own single write.
type safeWriter struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

// writeString writes str verbatim, unless a previous write on this
// safeWriter already failed. Safe for concurrent use.
func (s *safeWriter) writeString(str string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	_, s.err = io.WriteString(s.w, str)
}

// writeLine writes str followed by a newline as a single write, so it
// cannot be interleaved with a concurrent writeString/writeLine call on
// the same safeWriter.
func (s *safeWriter) writeLine(str string) {
	s.writeString(str + "\n")
}

// writeLinef formats and writes a line.
func (s *safeWriter) writeLinef(format string, args ...any) {
	s.writeLine(fmt.Sprintf(format, args...))
}
