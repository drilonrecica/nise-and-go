package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

func runFixture(args []string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, fixtureTree())
	return code, out.String(), errOut.String()
}

func TestExecuteNoArgsPrintsRootHelpAndExitsOK(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture(nil)
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitOK)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout = %q, want usage help", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExecuteHelpCommand(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"help"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "db") || !strings.Contains(stdout, "ping") {
		t.Errorf("stdout = %q, want it to list db and ping", stdout)
	}
}

func TestExecuteHelpForSpecificCommand(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"help", "ping"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "nise ping") {
		t.Errorf("stdout = %q, want it to name the command", stdout)
	}
}

func TestExecuteHelpForNestedCommand(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"help", "db", "migrate"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "nise db migrate") {
		t.Errorf("stdout = %q, want it to name the full nested path", stdout)
	}
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout = %q, want it to list the --dry-run flag", stdout)
	}
}

func TestExecuteCommandHelpFlag(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"db", "migrate", "--help"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "nise db migrate") {
		t.Errorf("stdout = %q, want it to name the command", stdout)
	}
}

func TestExecuteBareGroupCommandShowsHelp(t *testing.T) {
	t.Parallel()
	// "db" alone has no Run and Subcommands — dispatch shows its help
	// instead of erroring or doing nothing silently.
	code, stdout, _ := runFixture([]string{"db"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "migrate") || !strings.Contains(stdout, "backup") {
		t.Errorf("stdout = %q, want it to list migrate and backup", stdout)
	}
}

func TestExecuteNestedCommandRuns(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"db", "migrate"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "migrated\n" {
		t.Errorf("stdout = %q, want %q", stdout, "migrated\n")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExecuteNestedCommandOwnFlag(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"db", "migrate", "--dry-run"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "dry-run: migrated\n" {
		t.Errorf("stdout = %q, want %q", stdout, "dry-run: migrated\n")
	}
}

func TestExecuteNestedSiblingCommandRuns(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"db", "backup"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "backed up\n" {
		t.Errorf("stdout = %q, want %q", stdout, "backed up\n")
	}
}

func TestExecuteTopLevelUnknownCommandSuggestion(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"pign"}) // close to "ping"
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `"ping"`) {
		t.Errorf("stderr = %q, want a suggestion naming ping", stderr)
	}
}

func TestExecuteNestedUnknownSubcommandSuggestion(t *testing.T) {
	t.Parallel()
	code, _, stderr := runFixture([]string{"db", "migrat"}) // close to "migrate"
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
	if !strings.Contains(stderr, `"migrate"`) {
		t.Errorf("stderr = %q, want a suggestion naming migrate", stderr)
	}
	if !strings.Contains(stderr, "nise db") {
		t.Errorf("stderr = %q, want the error scoped to \"nise db\"", stderr)
	}
}

func TestExecuteUnknownCommandNoCloseMatch(t *testing.T) {
	t.Parallel()
	code, _, stderr := runFixture([]string{"zzzzzzzzzz"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
	if strings.Contains(stderr, "Did you mean") {
		t.Errorf("stderr = %q, want no suggestion for a wildly different input", stderr)
	}
}

func TestExecuteUnknownFlag(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"ping", "--bogus"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if stderr == "" {
		t.Error("stderr is empty, want a flag error")
	}
}

func TestExecuteQuietAndVerboseTogetherExitsUsage(t *testing.T) {
	t.Parallel()
	code, _, stderr := runFixture([]string{"--quiet", "--verbose", "ping"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d (usage error)", code, clierr.ExitUsage)
	}
	if stderr == "" {
		t.Error("stderr is empty, want the conflict explained")
	}
}

func TestExecuteQuietSuppressesResultOutput(t *testing.T) {
	t.Parallel()
	// Quiet never suppresses a command's actual Result output (only
	// incidental progress/prose) — ping's Result must still appear.
	code, stdout, _ := runFixture([]string{"--quiet", "ping"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "pong\n" {
		t.Errorf("stdout = %q, want %q even under --quiet", stdout, "pong\n")
	}
}

func TestExecuteQuietNeverSuppressesError(t *testing.T) {
	t.Parallel()
	code, _, stderr := runFixture([]string{"--quiet", "fail"})
	if code == int(clierr.ExitOK) {
		t.Fatal("exit code = 0, want a failure")
	}
	if stderr == "" {
		t.Error("--quiet suppressed an error, which the contract forbids")
	}
}

func TestExecuteExitCodesByErrorKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind string
		want clierr.ExitCode
	}{
		{"generic", clierr.ExitError},
		{"precondition", clierr.ExitPrecondition},
		{"plain", clierr.ExitError}, // a bare error, not a *clierr.Error, defaults to ExitError via clierr.From
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			code, _, _ := runFixture([]string{"fail", "--kind", tt.kind})
			if code != int(tt.want) {
				t.Errorf("kind=%s: exit code = %d, want %d", tt.kind, code, tt.want)
			}
		})
	}
}

func TestExecuteSuccessExitsZero(t *testing.T) {
	t.Parallel()
	code, _, _ := runFixture([]string{"ping"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestExecuteJSONSuccessParsesAndHasNoANSI is the brief's required
// end-to-end check for the success path, run through the real dispatcher
// (not just the output package in isolation).
func TestExecuteJSONSuccessParsesAndHasNoANSI(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"--json", "ping"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty (JSON errors go to stdout too)", stderr)
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Fatalf("stdout contains an ESC byte: %q", stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout did not parse as JSON: %v (%q)", err, stdout)
	}
}

// TestExecuteJSONErrorParsesAndHasNoANSI is the brief's required
// end-to-end check for the error path.
func TestExecuteJSONErrorParsesAndHasNoANSI(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"--json", "fail"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitError)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty — this task decided JSON-mode errors go to stdout", stderr)
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Fatalf("stdout contains an ESC byte: %q", stdout)
	}
	var env clierr.Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout did not parse as the error envelope: %v (%q)", err, stdout)
	}
	if env.Error.Message != "it failed" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "it failed")
	}
}

func TestExecuteJSONUnknownCommandIsAlsoJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"--json", "bogus"})
	if code != int(clierr.ExitUsage) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	var env clierr.Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout did not parse as JSON: %v (%q)", err, stdout)
	}
	if env.Error.Code != "usage_error" {
		t.Errorf("error.code = %q, want usage_error", env.Error.Code)
	}
}

func TestExecuteVersionFlagAlias(t *testing.T) {
	t.Parallel()
	// fixtureTree has no "version" command, so this proves --version is a
	// no-op (falls through to normal dispatch) when the registry lacks
	// one, rather than panicking. The real alias behavior against the
	// production registry is covered in cmd/nise's tests.
	code, _, stderr := runFixture([]string{"--version"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0 (falls through to root help)", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestExecuteGlobalFlagsWorkAfterCommandName(t *testing.T) {
	t.Parallel()
	// Global flags are recognized wherever they appear in argv, not just
	// before the command name.
	code, stdout, _ := runFixture([]string{"ping", "--json"})
	if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout did not parse as JSON: %v (%q)", err, stdout)
	}
}

// TestExecuteDoubleDashStopsGlobalFlagExtraction reproduces the reported
// bug: "nise <cmd> -- --verbose arg" must hand the command everything
// after "--" verbatim, not silently swallow "--verbose" as nise's own
// global flag and switch on verbose mode. This matters for any command
// that forwards trailing arguments to a subprocess (the planned dev and
// test commands both need it).
func TestExecuteDoubleDashStopsGlobalFlagExtraction(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"echoargs", "--", "--verbose", "extra"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "--verbose|extra") {
		t.Errorf("stdout = %q, want it to contain the literal, unstripped %q", stdout, "--verbose|extra")
	}
}

// TestExecuteDoubleDashBeforeCommandFlags proves "--" also protects a
// leaf's OWN flags from being mistaken for anything past that point —
// only global extraction is exercised above; this confirms the leaf's own
// flag.FlagSet.Parse (which has its own native "--" handling) sees nothing
// surprising either.
func TestExecuteDoubleDashBeforeCommandFlags(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"greet", "--", "--name", "world"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0", code)
	}
	// Everything after "--" is positional for greet's own flag set (Go's
	// flag package stops parsing flags at "--"), so --name is never
	// consumed as greet's own flag either — it shows up as a plain
	// positional argument, and the greeting is empty.
	if !strings.Contains(stdout, "hello ") {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestExecuteValueFlagArgumentIsNotStolenByGlobalExtraction reproduces the
// reported "argument stealing" bug: a value that happens to look like a
// reserved global flag spelling, when it is genuinely the value of one of
// the command's own value-consuming flags, must reach that flag intact —
// not be silently vacuumed up as the global flag of the same name (which
// previously left the value-consuming flag with no argument at all and
// failed the whole command with a confusing flag-parsing error).
func TestExecuteValueFlagArgumentIsNotStolenByGlobalExtraction(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"greet", "--name", "--json"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, stderr = %q, want 0 (the literal string \"--json\" as --name's value, not global JSON mode)", code, stderr)
	}
	if stdout != "hello --json\n" {
		t.Errorf("stdout = %q, want %q — --json must be preserved as --name's literal value", stdout, "hello --json\n")
	}
}

// TestExecuteValueFlagStolenValueDoesNotFlipMode is the stronger form of
// the test above: it also proves the output MODE itself was not
// incorrectly switched to JSON by the same stray "--json" token, since
// extractGlobalFlagsAware recomputes the writer from the aware pass, not
// the initial blind one.
func TestExecuteValueFlagStolenValueDoesNotFlipMode(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runFixture([]string{"greet", "--name", "--json"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Errorf("stdout = %q, looks like JSON — the stolen --json token flipped output mode", stdout)
	}
}

// TestExecuteReservedFlagCollisionIsALoudFailure reproduces the reported
// silent-shadowing bug: a command that defines its own "--quiet" flag
// must never be allowed to run in a way where the command's own variable
// silently stays false while the *global* --quiet activates instead. It
// must fail loudly, every time, regardless of whether --quiet was even
// passed on this particular invocation.
func TestExecuteReservedFlagCollisionIsALoudFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{"invoked without the colliding flag", []string{"shadow"}},
		{"invoked with the colliding flag", []string{"shadow", "--quiet"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// A fresh root per subtest: shadowCommand's NewFlagSet closes
			// over one "var quiet bool", and two parallel subtests
			// dispatching against the SAME *Command would race on it —
			// exactly the kind of shared-fixture bug this file must not
			// have, since production Commands() is always rebuilt fresh
			// per Execute() call for the same reason.
			root := []*Command{shadowCommand()}
			var out, errOut bytes.Buffer
			code := execute(tt.args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, root)
			if code == int(clierr.ExitOK) {
				t.Fatalf("exit code = 0, want a loud failure (got stdout=%q)", out.String())
			}
			if out.String() != "" {
				t.Errorf("stdout = %q, want empty — the command must never actually run", out.String())
			}
			if !strings.Contains(errOut.String(), "quiet") {
				t.Errorf("stderr = %q, want it to name the colliding flag", errOut.String())
			}
		})
	}
}

// TestExecuteReservedFlagCollisionViaHelp proves the same loud failure
// happens for "nise help shadow" and "nise shadow --help" — a command
// this broken should not even render help successfully, since its own
// flag listing is part of what's broken.
func TestExecuteReservedFlagCollisionViaHelp(t *testing.T) {
	t.Parallel()
	root := []*Command{shadowCommand()}

	for _, args := range [][]string{{"help", "shadow"}, {"shadow", "--help"}} {
		var out, errOut bytes.Buffer
		code := execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, root)
		if code == int(clierr.ExitOK) {
			t.Errorf("args=%v: exit code = 0, want a loud failure", args)
		}
		if !strings.Contains(errOut.String(), "quiet") {
			t.Errorf("args=%v: stderr = %q, want it to name the colliding flag", args, errOut.String())
		}
	}
}

// TestExecuteDoubleDashBeforeAnyLeafIsKnown isolates extractGlobalFlags's
// own "--" handling (as opposed to extractGlobalFlagsAware's, which also
// stops at "--" but only runs once a leaf is already resolved). The
// --quiet/--verbose conflict check runs on the very first, blind
// extraction pass, before any command is resolved — so if that pass
// didn't stop at "--", "nise ping --quiet -- --verbose" would
// (wrongly) see both flags set and reject the combination before ever
// reaching ping, even though the user's actual --verbose here is a
// literal argument after "--", not nise's own global flag.
func TestExecuteDoubleDashBeforeAnyLeafIsKnown(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runFixture([]string{"ping", "--quiet", "--", "--verbose"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, stderr = %q, want 0 (no false --quiet/--verbose conflict)", code, stderr)
	}
	if stdout != "pong\n" {
		t.Errorf("stdout = %q, want %q (--quiet suppresses only progress output, never Result)", stdout, "pong\n")
	}
}
