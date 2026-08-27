package output

import (
	"errors"
	"testing"
)

// failingWriter fails every write, so safeWriter's "stop after first error"
// behavior is directly observable.
type failingWriter struct {
	calls int
	err   error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.calls++
	return 0, f.err
}

func TestSafeWriterStopsAfterFirstError(t *testing.T) {
	t.Parallel()
	fw := &failingWriter{err: errors.New("boom")}
	sw := &safeWriter{w: fw}

	sw.writeString("a")
	sw.writeString("b")
	sw.writeLine("c")

	if fw.calls != 1 {
		t.Errorf("underlying writer was called %d times, want exactly 1 (stop after first error)", fw.calls)
	}
	if sw.err == nil {
		t.Error("safeWriter.err is nil after a failing write")
	}
}
