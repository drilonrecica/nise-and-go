package output

import (
	"sync"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

// ANSI codes used only by this file. This is deliberate: json.go must never
// contain an escape sequence, and keeping every ANSI literal in one
// human-only file makes that easy to audit (see json_source_test.go, which
// greps json.go for exactly this kind of literal).
const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[32m"
	ansiBold  = "\x1b[1m"
	ansiClear = "\x1b[K"
)

// humanWriter renders prose to stdout and errors to stderr, with color and
// animation gated by the flags resolved into it at construction time. All
// writes go through the embedded *safeWriter — see safewrite.go — rather
// than a raw fmt.Fprint* call, so an errcheck-flagged unchecked write
// return value cannot recur here.
type humanWriter struct {
	stdout  *safeWriter
	stderr  *safeWriter
	quiet   bool
	verbose bool
	color   bool
	animate bool
}

func (w *humanWriter) Mode() Mode { return ModeHuman }

func (w *humanWriter) Line(msg string) {
	if w.quiet {
		return
	}
	w.stdout.writeLine(msg)
}

func (w *humanWriter) Verbosef(format string, args ...any) {
	if w.quiet || !w.verbose {
		return
	}
	w.stdout.writeLinef(format, args...)
}

func (w *humanWriter) Success(msg string) {
	if w.quiet {
		return
	}
	w.stdout.writeLine(w.style(ansiGreen, msg))
}

func (w *humanWriter) Banner(text string) {
	if !w.personalityEnabled() {
		return
	}
	w.stdout.writeLine(w.style(ansiBold, text))
}

func (w *humanWriter) Result(v Result) {
	w.stdout.writeLine(v.Human())
}

func (w *humanWriter) Error(err error) {
	ce := clierr.From(err)
	for _, line := range ce.HumanLines(w.verbose) {
		w.stderr.writeLine(line)
	}
}

// style wraps text in code/reset when color is enabled, and returns text
// unchanged otherwise. It is the only place in this file that decides
// whether an ANSI code is actually emitted.
func (w *humanWriter) style(code, text string) string {
	if !w.color {
		return text
	}
	return code + text + ansiReset
}

// personalityEnabled reports whether the restrained-Astro-like extras
// (banner, spinner) are allowed right now: human mode is implicit (this is
// humanWriter), and additionally not quiet and animation must be enabled.
func (w *humanWriter) personalityEnabled() bool {
	return w.animate && !w.quiet
}

// Spinner starts a progress indicator, or returns a no-op handle when
// personality effects are not appropriate right now (quiet, non-interactive,
// CI, TERM=dumb — anything AnimationEnabled or --quiet ruled out upstream).
// Either way, Stop always ends by printing msg through Success, so calling
// code never branches on whether the spinner actually animated.
func (w *humanWriter) Spinner(label string) Spinner {
	if !w.personalityEnabled() {
		return &staticSpinner{w: w}
	}
	s := &liveSpinner{w: w, stop: make(chan struct{}), done: make(chan struct{})}
	go s.run(label)
	return s
}

// staticSpinner is the no-animation Spinner: Start does nothing (there is
// no Start method — construction is the start), and Stop just reports
// completion the same way any other command output does. Stop is
// idempotent (see liveSpinner's doc comment for why that contract matters
// for every Spinner implementation, not just the animated one).
type staticSpinner struct {
	w    *humanWriter
	once sync.Once
}

func (s *staticSpinner) Stop(msg string) {
	s.once.Do(func() { s.w.Success(msg) })
}

// liveSpinner ticks a small frame animation on stdout until Stop is called.
// Stop closes stop and blocks on done, so by the time Stop's own write
// happens the goroutine is guaranteed to have made its last write — no
// completed operation is ever held up waiting for the next animation frame.
//
// Stop is idempotent, guarded by once: the natural calling pattern for a
// long-running command is `defer sp.Stop(...)` alongside an explicit Stop
// on the happy path, which is two calls. Without the guard, the second
// call's close(s.stop) on an already-closed channel panics and crashes the
// whole CLI — every Spinner implementation (this one, staticSpinner, and
// json.go's noopSpinner) honors "Stop may be called more than once; only
// the first call's message is shown" for exactly this reason.
type liveSpinner struct {
	w    *humanWriter
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

var spinnerFrames = []rune{'|', '/', '-', '\\'}

func (s *liveSpinner) run(label string) {
	defer close(s.done)
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.w.stdout.writeString(string('\r') + string(spinnerFrames[i%len(spinnerFrames)]) + " " + label)
			i++
		}
	}
}

func (s *liveSpinner) Stop(msg string) {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.w.stdout.writeString("\r" + ansiClear)
		s.w.Success(msg)
	})
}
