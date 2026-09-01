package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/release"
)

// serveRelease starts a stand-in release index. Every test here talks to it
// rather than to GitHub: the one command in nise that opens a socket is still
// a command whose tests must pass offline.
func serveRelease(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func releaseBody(tag string) string {
	return `{"tag_name":"` + tag + `","html_url":"https://github.com/drilonrecica/nise-and-go/releases/tag/` +
		tag + `","draft":false,"prerelease":false}`
}

func runVersionCheck(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(args, IO{Stdout: &stdout, Stderr: &stderr, Getenv: emptyGetenv})
	return code, stdout.String(), stderr.String()
}

// A binary built from a checkout reports "dev", which no parser can compare —
// and telling that person a release exists is the useful answer, not an error.
func TestVersionCheckReportsAnAvailableRelease(t *testing.T) {
	t.Parallel()
	url := serveRelease(t, releaseBody("v9.9.9"))

	code, stdout, stderr := runVersionCheck(t, "version", "check", "-url", url)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "v9.9.9") {
		t.Errorf("the output does not name the available release: %q", stdout)
	}
	// The promise that makes an update check safe to run.
	if !strings.Contains(stdout, "changed nothing") {
		t.Errorf("the output does not say nise changed nothing: %q", stdout)
	}
}

func TestVersionCheckSaysWhenNothingIsNewer(t *testing.T) {
	t.Parallel()
	// v0.0.0 can never be newer than any build, including "dev".
	url := serveRelease(t, releaseBody("v0.0.0"))

	code, stdout, _ := runVersionCheck(t, "version", "check", "-url", url)
	if code != 0 {
		t.Fatalf("exit code %d, want 0: %q", code, stdout)
	}
	if !strings.Contains(stdout, "latest release") {
		t.Errorf("the output does not say the installed version is current: %q", stdout)
	}
	// Nothing to do means no instructions: a person who is up to date should
	// not have to read a command they must not run.
	if strings.Contains(stdout, "brew") || strings.Contains(stdout, "go install") {
		t.Errorf("an up-to-date check printed upgrade instructions: %q", stdout)
	}
}

// The whole point of detecting a channel is printing one instruction rather
// than four. A wrong one is worse than none: `brew upgrade` on a binary
// Homebrew does not own does nothing, and reads like a bug in nise.
func TestEachChannelGetsTheInstructionThatWorks(t *testing.T) {
	t.Parallel()

	cases := map[release.Channel]string{
		release.ChannelHomebrew:  "brew update && brew upgrade nise",
		release.ChannelGoInstall: "go install github.com/drilonrecica/nise-and-go/cmd/nise@v9.9.9",
		release.ChannelArchive:   "gh release download v9.9.9 --repo drilonrecica/nise-and-go",
		release.ChannelSource:    "git -C <your checkout> fetch --tags && git checkout v9.9.9",
	}
	for channel, want := range cases {
		t.Run(string(channel), func(t *testing.T) {
			t.Parallel()
			got := upgradeInstructions(channel, "v9.9.9")
			if !contains(got, want) {
				t.Errorf("the %s channel is told %v, and the command that works is %q", channel, got, want)
			}
			// One channel, one action. The unknown case is the exception,
			// and it says so.
			for other, instruction := range cases {
				if other != channel && contains(got, instruction) {
					t.Errorf("the %s channel is also told to run %q, which is the %s channel's command",
						channel, instruction, other)
				}
			}
		})
	}
}

// An honest "I could not tell" beats a confident wrong instruction.
func TestAnUnknownChannelIsToldSoAndGivenEveryOption(t *testing.T) {
	t.Parallel()
	got := upgradeInstructions(release.ChannelUnknown, "v9.9.9")

	if !contains(got, "nise could not tell how it was installed. Use whichever of these matches:") {
		t.Errorf("the unknown channel is not told that nise could not tell: %v", got)
	}
	for _, instruction := range []string{"brew", "go install", "gh release download"} {
		if !containsSubstring(got, instruction) {
			t.Errorf("the unknown channel is not offered the %s route: %v", instruction, got)
		}
	}
}

func TestVersionCheckJSONShape(t *testing.T) {
	t.Parallel()
	url := serveRelease(t, releaseBody("v9.9.9"))

	code, stdout, stderr := runVersionCheck(t, "--json", "version", "check", "-url", url)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	var got versionCheckResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("the output is not one JSON document: %v\n%s", err, stdout)
	}
	if got.Latest != "v9.9.9" || !got.Outdated {
		t.Errorf("result = %+v, want latest v9.9.9 and outdated", got)
	}
	if got.Channel == "" {
		t.Error("the result names no installation channel")
	}
	if len(got.HowTo) == 0 {
		t.Error("the result carries no instructions for a check that found a newer release")
	}
}

// The recovery line matters more here than almost anywhere in the CLI: the
// most common cause is having no network, and a person who ran the one command
// that needs one should not be left guessing which.
func TestAnUnreachableIndexSaysWhatToDo(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	server.Close() // closed before use: nothing is listening.

	code, _, stderr := runVersionCheck(t, "version", "check", "-url", server.URL)
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr, "could not reach the release index") {
		t.Errorf("stderr does not say what failed: %q", stderr)
	}
	if !strings.Contains(stderr, "everything else works offline") {
		t.Errorf("stderr does not tell the reader this is the only command needing a network: %q", stderr)
	}
}

// A repository with no releases is not a broken installation, and the message
// has to say so or it reads as one.
func TestNoPublishedReleaseIsExplained(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	_, _, stderr := runVersionCheck(t, "version", "check", "-url", server.URL)
	if !strings.Contains(stderr, "Nothing is wrong with your installation") {
		t.Errorf("stderr reads like a failure of the installation: %q", stderr)
	}
}

// The command is reachable where a reader would look for it, and `nise
// version` on its own still prints the version rather than help.
func TestVersionStillPrintsTheVersionAndListsTheCheck(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runVersionCheck(t, "version")
	if code != 0 {
		t.Fatalf("nise version exited %d", code)
	}
	if !strings.HasPrefix(stdout, "nise ") {
		t.Errorf("nise version no longer prints the version: %q", stdout)
	}

	_, help, _ := runVersionCheck(t, "version", "--help")
	if !strings.Contains(help, "check") {
		t.Errorf("nise version --help does not list the check subcommand: %q", help)
	}
	if !strings.Contains(help, "network") {
		t.Errorf("the check's summary does not say it uses the network, which is the one thing a reader needs to know before running it: %q", help)
	}
}

func contains(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func containsSubstring(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}
