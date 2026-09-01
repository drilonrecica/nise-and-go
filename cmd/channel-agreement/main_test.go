package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testTag    = "v0.1.0"
	testCommit = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	testDate   = "2026-09-01T00:00:00Z"
)

// reports builds a directory of per-channel reports in the layout
// actions/download-artifact produces: one directory per artifact, each holding
// channel.json.
type reports struct {
	t   *testing.T
	dir string
}

func newReports(t *testing.T) *reports {
	t.Helper()
	r := &reports{t: t, dir: t.TempDir()}
	for _, channel := range channels {
		for _, runner := range channel.runners {
			commit, date := testCommit, testDate
			if !channel.stamped {
				// A `go install` binary is built from a module zip, which
				// carries no VCS metadata for the toolchain to record.
				commit, date = "", ""
			}
			r.set(channel.name, runner, testTag, commit, date)
		}
	}
	return r
}

func (r *reports) set(channel, runner, version, commit, date string) {
	r.t.Helper()
	dir := filepath.Join(r.dir, "channel-"+channel+"-"+runner)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.t.Fatalf("creating %s: %v", dir, err)
	}
	body := fmt.Sprintf(`{"version":%q,"commit":%q,"date":%q}`, version, commit, date)
	// #nosec G703 -- dir is under this test's own t.TempDir().
	if err := os.WriteFile(filepath.Join(dir, reportName), []byte(body), 0o600); err != nil {
		r.t.Fatalf("writing %s: %v", reportName, err)
	}
}

func (r *reports) remove(channel, runner string) {
	r.t.Helper()
	if err := os.RemoveAll(filepath.Join(r.dir, "channel-"+channel+"-"+runner)); err != nil {
		r.t.Fatalf("removing %s/%s: %v", channel, runner, err)
	}
}

func check(t *testing.T, r *reports) []string {
	t.Helper()
	problems, err := compare(testTag, r.dir)
	if err != nil {
		t.Fatalf("the comparison could not look: %v", err)
	}
	return problems
}

func mustFind(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p, want) {
			return
		}
	}
	t.Errorf("no problem mentioned %q; the comparison reported %v", want, problems)
}

func TestChannelsThatAgreePass(t *testing.T) {
	t.Parallel()
	if problems := check(t, newReports(t)); len(problems) != 0 {
		t.Fatalf("agreeing channels were reported as disagreeing: %v", problems)
	}
}

// The failure this command exists for. Both channels installed something that
// runs; they installed different things.
func TestAChannelInstallingAnotherVersionIsReported(t *testing.T) {
	t.Parallel()
	r := newReports(t)
	r.set("homebrew", "macos-latest", "v0.0.9", testCommit, testDate)
	mustFind(t, check(t, r), `homebrew on macos-latest installed "v0.0.9"`)
}

// A stale formula pointing at the previous release is how this actually
// happens, and it is invisible from inside any single channel: brew installs,
// brew runs, brew reports a perfectly good version.
func TestChannelsInstallingDifferentCommitsAreReported(t *testing.T) {
	t.Parallel()
	r := newReports(t)
	r.set("homebrew", "ubuntu-latest", testTag, strings.Repeat("9", 40), testDate)
	mustFind(t, check(t, r), "built from")
}

// The failure that makes a comparison worthless: comparing only the channels
// that turned up. A job that failed uploads nothing, so silence here would
// mean this command passes most loudly when the most is broken.
func TestAChannelThatDidNotReportIsAFailure(t *testing.T) {
	t.Parallel()
	for _, channel := range channels {
		for _, runner := range channel.runners {
			t.Run(channel.name+"/"+runner, func(t *testing.T) {
				t.Parallel()
				r := newReports(t)
				r.remove(channel.name, runner)
				mustFind(t, check(t, r), channel.name+" on "+runner+" reported nothing")
			})
		}
	}
}

// `go install` cannot report a commit — a module zip carries no VCS metadata
// — so requiring one there would fail every correct release. Requiring it of
// the archive-installing channels is what catches a binary built outside the
// release workflow.
func TestOnlyTheArchiveChannelsMustCarryProvenance(t *testing.T) {
	t.Parallel()

	t.Run("go install without a commit is fine", func(t *testing.T) {
		t.Parallel()
		r := newReports(t)
		r.set("go-install", "ubuntu-latest", testTag, "", "")
		if problems := check(t, r); len(problems) != 0 {
			t.Errorf("a go install report without VCS metadata was reported as a problem: %v", problems)
		}
	})
	t.Run("an archive without a commit is not", func(t *testing.T) {
		t.Parallel()
		r := newReports(t)
		r.set("archive", "windows-latest", testTag, "", "")
		mustFind(t, check(t, r), "reports no commit")
	})
	t.Run("an archive without a build date is not", func(t *testing.T) {
		t.Parallel()
		r := newReports(t)
		r.set("archive", "windows-latest", testTag, testCommit, "")
		mustFind(t, check(t, r), "reports no build date")
	})
}

// The version every channel is compared against is the tag, "v" included.
// That is the form `go install` reports and the form internal/version
// documents; a release archive stamped with the bare number is one channel
// disagreeing with another about what the release is called.
func TestTheComparisonIsAgainstTheTagIncludingItsV(t *testing.T) {
	t.Parallel()
	r := newReports(t)
	for _, channel := range channels {
		for _, runner := range channel.runners {
			commit, date := testCommit, testDate
			if !channel.stamped {
				commit, date = "", ""
			}
			r.set(channel.name, runner, strings.TrimPrefix(testTag, "v"), commit, date)
		}
	}
	problems := check(t, r)
	if len(problems) == 0 {
		t.Fatal("every channel reporting the bare version number was accepted")
	}
	mustFind(t, problems, `installed "0.1.0"`)
}

// Being unable to look is not agreement.
func TestBeingUnableToLookIsNotSuccess(t *testing.T) {
	t.Parallel()
	dir := newReports(t).dir

	cases := []struct {
		name string
		args []string
	}{
		{"no tag", []string{"-dir", dir}},
		{"no directory", []string{"-tag", testTag}},
		{"tag is not a release tag", []string{"-tag", "0.1.0", "-dir", dir}},
		{"directory does not exist", []string{"-tag", testTag, "-dir", filepath.Join(dir, "nowhere")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr); code != 2 {
				t.Errorf("exit code %d; a comparison that could not look must not exit 0 or 1 (stdout %q)", code, stdout.String())
			}
		})
	}
}

func TestAMalformedReportIsReported(t *testing.T) {
	t.Parallel()
	r := newReports(t)
	dir := filepath.Join(r.dir, "channel-archive-ubuntu-latest")
	// #nosec G703 -- dir is under this test's own t.TempDir().
	if err := os.WriteFile(filepath.Join(dir, reportName), []byte("not json"), 0o600); err != nil {
		t.Fatalf("writing a malformed report: %v", err)
	}
	mustFind(t, check(t, r), "is not the JSON")
}

func TestTheExitCodesAreDistinct(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-tag", testTag, "-dir", newReports(t).dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("agreeing channels exited %d: %s", code, stdout.String())
	}

	broken := newReports(t)
	broken.remove("archive", "macos-latest")
	stdout.Reset()
	if code := run([]string{"-tag", testTag, "-dir", broken.dir}, &stdout, &stderr); code != 1 {
		t.Errorf("disagreeing channels exited %d, and disagreement is exit 1", code)
	}
	if !strings.Contains(stdout.String(), "do not agree") {
		t.Errorf("the summary line is missing from %q", stdout.String())
	}
}

// Every channel the documentation names is exercised. A channel silently
// dropped from this table would stop being tested without anything failing.
func TestEveryDocumentedChannelIsCompared(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"archive":    {"ubuntu-latest", "macos-latest", "windows-latest"},
		"go-install": {"ubuntu-latest", "macos-latest", "windows-latest"},
		// Homebrew does not run on Windows.
		"homebrew": {"ubuntu-latest", "macos-latest"},
	}
	if len(channels) != len(want) {
		t.Fatalf("this project supports %d installation channels and %d are compared", len(want), len(channels))
	}
	for _, channel := range channels {
		runners, ok := want[channel.name]
		if !ok {
			t.Errorf("%s is compared and is not a documented installation channel", channel.name)
			continue
		}
		if strings.Join(channel.runners, " ") != strings.Join(runners, " ") {
			t.Errorf("%s is exercised on %v, and it serves %v", channel.name, channel.runners, runners)
		}
	}
}
