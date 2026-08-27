package output

import (
	"fmt"
	"io"
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
type safeWriter struct {
	w   io.Writer
	err error
}

// writeString writes str verbatim, unless a previous write on this
// safeWriter already failed.
func (s *safeWriter) writeString(str string) {
	if s.err != nil {
		return
	}
	_, s.err = io.WriteString(s.w, str)
}

// writeLine writes str followed by a newline.
func (s *safeWriter) writeLine(str string) {
	s.writeString(str)
	s.writeString("\n")
}

// writeLinef formats and writes a line.
func (s *safeWriter) writeLinef(format string, args ...any) {
	s.writeLine(fmt.Sprintf(format, args...))
}
