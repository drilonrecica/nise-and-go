package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

func TestHumanWriterLineQuietSuppression(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	w := New(ModeHuman, Options{Quiet: true}, &out, &bytes.Buffer{})
	w.Line("progress")
	w.Success("done")
	w.Verbosef("detail")
	if out.Len() != 0 {
		t.Errorf("quiet mode wrote informational output: %q", out.String())
	}
}

func TestHumanWriterQuietNeverSuppressesError(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	w := New(ModeHuman, Options{Quiet: true}, &bytes.Buffer{}, &stderr)
	w.Error(clierr.Usage("bad command", "run help"))
	if stderr.Len() == 0 {
		t.Error("quiet mode suppressed an error, which the contract forbids")
	}
	if !strings.Contains(stderr.String(), "bad command") {
		t.Errorf("stderr = %q, want it to contain the cause", stderr.String())
	}
}

func TestHumanWriterErrorGoesToStderrNotStdout(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	w := New(ModeHuman, Options{}, &stdout, &stderr)
	w.Error(clierr.New(clierr.ExitError, "boom", "try again"))
	if stdout.Len() != 0 {
		t.Errorf("human mode wrote an error to stdout: %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("human mode wrote nothing to stderr for an error")
	}
}

func TestHumanWriterResultGoesToStdout(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	w := New(ModeHuman, Options{}, &stdout, &bytes.Buffer{})
	w.Result(fixtureResult{Name: "doctor"})
	if got := stdout.String(); got != "name: doctor\n" {
		t.Errorf("Result output = %q, want %q", got, "name: doctor\n")
	}
}

func TestHumanWriterVerboseGating(t *testing.T) {
	t.Parallel()

	var withoutVerbose bytes.Buffer
	New(ModeHuman, Options{Verbose: false}, &withoutVerbose, &bytes.Buffer{}).Verbosef("detail %d", 1)
	if withoutVerbose.Len() != 0 {
		t.Errorf("Verbosef wrote output without --verbose: %q", withoutVerbose.String())
	}

	var withVerbose bytes.Buffer
	New(ModeHuman, Options{Verbose: true}, &withVerbose, &bytes.Buffer{}).Verbosef("detail %d", 1)
	if withVerbose.String() != "detail 1\n" {
		t.Errorf("Verbosef output = %q, want %q", withVerbose.String(), "detail 1\n")
	}
}

func TestHumanWriterErrorChainShownOnlyUnderVerbose(t *testing.T) {
	t.Parallel()
	ce := clierr.Wrap(errors.New("dial tcp: refused"), clierr.ExitPrecondition, "could not reach the database", "start postgres")

	var quiet bytes.Buffer
	New(ModeHuman, Options{Verbose: false}, &bytes.Buffer{}, &quiet).Error(ce)
	if strings.Contains(quiet.String(), "dial tcp") {
		t.Errorf("non-verbose human error output contains the underlying chain: %q", quiet.String())
	}

	var verbose bytes.Buffer
	New(ModeHuman, Options{Verbose: true}, &bytes.Buffer{}, &verbose).Error(ce)
	if !strings.Contains(verbose.String(), "dial tcp") {
		t.Errorf("verbose human error output missing the underlying chain: %q", verbose.String())
	}
}

func TestHumanWriterColorGating(t *testing.T) {
	t.Parallel()

	var colored bytes.Buffer
	New(ModeHuman, Options{Color: true}, &colored, &bytes.Buffer{}).Success("done")
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Errorf("Color:true Success output has no ANSI: %q", colored.String())
	}

	var plain bytes.Buffer
	New(ModeHuman, Options{Color: false}, &plain, &bytes.Buffer{}).Success("done")
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("Color:false Success output has ANSI: %q", plain.String())
	}
}

func TestHumanWriterBannerGating(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    Options
		wantOut bool
	}{
		{"animate and not quiet: shown", Options{Animate: true, Quiet: false}, true},
		{"quiet: suppressed even if animate", Options{Animate: true, Quiet: true}, false},
		{"not animate: suppressed", Options{Animate: false, Quiet: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			New(ModeHuman, tt.opts, &out, &bytes.Buffer{}).Banner("nise")
			if got := out.Len() > 0; got != tt.wantOut {
				t.Errorf("Banner wrote output = %v, want %v (buf=%q)", got, tt.wantOut, out.String())
			}
		})
	}
}

func TestHumanWriterStaticSpinnerStopsImmediatelyWithMessage(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	// Animate: false forces the no-op static spinner path.
	w := New(ModeHuman, Options{Animate: false}, &out, &bytes.Buffer{})
	sp := w.Spinner("working")
	sp.Stop("finished")
	if !strings.Contains(out.String(), "finished") {
		t.Errorf("static spinner Stop did not print the final message: %q", out.String())
	}
}

func TestHumanWriterLiveSpinnerStopPrintsFinalMessageAndStopsWriting(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	w := New(ModeHuman, Options{Animate: true, Quiet: false}, &out, &bytes.Buffer{})
	sp := w.Spinner("working")

	// Let at least one 120ms animation tick actually happen before
	// stopping. Calling Stop immediately (as this test used to) proves
	// nothing about whether Stop is held behind a frame boundary in
	// steady state — it would pass identically whether or not that
	// guarantee held, since there'd never have been a frame in flight to
	// wait for in the first place.
	time.Sleep(150 * time.Millisecond)

	stopStart := time.Now()
	sp.Stop("finished")
	stopElapsed := time.Since(stopStart)

	// Stop must return quickly — well under the 120ms tick interval —
	// rather than blocking until the next scheduled frame. A generous
	// margin (half a tick) keeps this robust under -race's slowdown and
	// CI scheduling jitter while still failing if Stop were, say, waiting
	// on ticker.C instead of its own stop/done channels.
	if stopElapsed > 60*time.Millisecond {
		t.Errorf("Stop took %s, want well under one 120ms animation frame — a completed operation must not wait for the next tick", stopElapsed)
	}

	final := out.String()
	if !strings.Contains(final, "finished") {
		t.Fatalf("live spinner Stop did not print the final message: %q", final)
	}

	// Stop must block until the animation goroutine has made its last
	// write, so nothing further gets appended after Stop returns. Give a
	// hypothetical stray delayed write a real window to land before
	// asserting the buffer is unchanged.
	lenAfterStop := len(final)
	time.Sleep(150 * time.Millisecond)
	if len(out.String()) != lenAfterStop {
		t.Error("spinner wrote more output after Stop returned — it was not fully stopped")
	}
}

func TestHumanWriterLiveSpinnerDoubleStopIsIdempotent(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	w := New(ModeHuman, Options{Animate: true}, &out, &bytes.Buffer{})
	sp := w.Spinner("working")

	// The natural usage pattern is `defer sp.Stop(...)` plus an explicit
	// Stop on the happy path — two calls. Before the idempotency fix,
	// the second call's close(s.stop) on an already-closed channel
	// panicked and crashed the whole CLI; this must not panic.
	sp.Stop("first")
	sp.Stop("second")

	got := out.String()
	if strings.Count(got, "first") != 1 {
		t.Errorf("output = %q, want %q printed exactly once", got, "first")
	}
	if strings.Contains(got, "second") {
		t.Errorf("output = %q, want the second Stop call's message ignored", got)
	}
}

func TestHumanWriterStaticSpinnerDoubleStopIsIdempotent(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	w := New(ModeHuman, Options{Animate: false}, &out, &bytes.Buffer{})
	sp := w.Spinner("working")

	sp.Stop("first")
	sp.Stop("second")

	got := out.String()
	if strings.Count(got, "first") != 1 {
		t.Errorf("output = %q, want %q printed exactly once", got, "first")
	}
	if strings.Contains(got, "second") {
		t.Errorf("output = %q, want the second Stop call's message ignored", got)
	}
}

// TestHumanWriterSpinnerRaceWithConcurrentWrites reproduces the reported
// race: a live spinner's animation goroutine and the calling goroutine's
// own Line/Result/etc. calls both write through the same humanWriter,
// which is exactly the normal way a long-running command (doctor, dev,
// check) reports progress alongside a spinner. Run with -race; before the
// safeWriter synchronization fix this reliably reported a data race on
// safeWriter.err and interleaved output.
func TestHumanWriterSpinnerRaceWithConcurrentWrites(t *testing.T) {
	var out bytes.Buffer
	w := New(ModeHuman, Options{Animate: true}, &out, &bytes.Buffer{})
	sp := w.Spinner("working")

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(150 * time.Millisecond)
		for time.Now().Before(deadline) {
			w.Line("progress")
		}
	}()

	// Give the spinner's ticker goroutine and the writer goroutine above
	// real overlapping wall-clock time before stopping either.
	time.Sleep(150 * time.Millisecond)
	<-done
	sp.Stop("finished")

	if !strings.Contains(out.String(), "finished") {
		t.Error("expected the final message in output")
	}
}

func TestHumanWriterMode(t *testing.T) {
	t.Parallel()
	w := New(ModeHuman, Options{}, &bytes.Buffer{}, &bytes.Buffer{})
	if w.Mode() != ModeHuman {
		t.Errorf("Mode() = %v, want ModeHuman", w.Mode())
	}
}
