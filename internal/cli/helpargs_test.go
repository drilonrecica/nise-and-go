package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandHelpRendersPositionalArguments covers the contract gap this fix
// wave closed: Command had no field for a positional argument, so
// `nise new --help` printed "nise new [flags]" and a user reading only
// --help could not discover how to name their project, while
// `nise generate feature --help` advertised flags it does not have and
// never mentioned the one argument it requires.
func TestCommandHelpRendersPositionalArguments(t *testing.T) {
	tests := []struct {
		name       string
		path       []string
		wantUsage  string
		wantFlags  bool
		wantNoSect string
	}{
		{
			name:      "new takes an optional name and has flags",
			path:      []string{"new"},
			wantUsage: "nise new [name] [flags]",
			wantFlags: true,
		},
		{
			name:      "generate feature takes a required name and has no flags",
			path:      []string{"generate", "feature"},
			wantUsage: "nise generate feature <name>",
		},
		{
			name:      "generate resource takes a required name",
			path:      []string{"generate", "resource"},
			wantUsage: "nise generate resource <name>",
		},
		{
			name:      "a group command still advertises its subcommands",
			path:      []string{"generate"},
			wantUsage: "nise generate <subcommand>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := resolveForTest(t, tc.path)
			h := commandHelp(tc.path, cmd)
			if h.Usage != tc.wantUsage {
				t.Errorf("Usage = %q, want %q", h.Usage, tc.wantUsage)
			}
			if got := len(h.Flags) > 0; got != tc.wantFlags {
				t.Errorf("has flags = %v, want %v", got, tc.wantFlags)
			}
			if !tc.wantFlags && strings.Contains(h.Human(), "\nFlags:\n") {
				t.Error("rendered a Flags: section for a command with no flags of its own")
			}
		})
	}
}

// TestCommandsWithoutPositionalArgumentsAreUnchanged pins that the new field
// is opt-in: the six commands that take no positional argument must render
// exactly as they did before it existed.
func TestCommandsWithoutPositionalArgumentsAreUnchanged(t *testing.T) {
	want := map[string]string{
		"version": "nise version",
		"doctor":  "nise doctor",
		"agents":  "nise agents [flags]",
		"test":    "nise test [flags]",
		"dev":     "nise dev [flags]",
		"check":   "nise check [flags]",
	}
	for name, wantUsage := range want {
		t.Run(name, func(t *testing.T) {
			cmd := resolveForTest(t, []string{name})
			if cmd.Args != "" {
				t.Fatalf("Args = %q, want empty for a command with no positional argument", cmd.Args)
			}
			if got := commandHelp([]string{name}, cmd).Usage; got != wantUsage {
				t.Errorf("Usage = %q, want %q", got, wantUsage)
			}
		})
	}
}

// TestUnknownDashTokenIsReportedAsAFlag covers the wrong-noun defect:
// `nise -json version` answered `unknown command "-json"`, sending the
// reader to look for a command they never typed.
func TestUnknownDashTokenIsReportedAsAFlag(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantKind string
	}{
		{name: "single-dash global flag", token: "-json", wantKind: "unknown flag"},
		{name: "unknown double-dash flag", token: "--nope", wantKind: "unknown flag"},
		{name: "a bare dash is not a flag", token: "-", wantKind: "unknown command"},
		{name: "an ordinary typo is still a command", token: "doctro", wantKind: "unknown command"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := unknownCommandError(nil, tc.token, names(Commands()))
			if !strings.HasPrefix(err.Cause(), tc.wantKind) {
				t.Errorf("cause = %q, want it to start with %q", err.Cause(), tc.wantKind)
			}
			if !strings.Contains(err.Cause(), tc.token) {
				t.Errorf("cause = %q, want it to name the token %q", err.Cause(), tc.token)
			}
		})
	}
}

// TestGoSuiteMatchesTheGeneratedMakefile pins the fix for the leaked
// `go: warning: "./db/..." matched no packages`. Two commands that both
// claim to run the project's Go tests must not run different package sets,
// and the Makefile is the one with the reasoning written down.
func TestGoSuiteMatchesTheGeneratedMakefile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := planGoSuite(dir)
	if !plan.Runnable {
		t.Fatalf("planGoSuite not runnable with a go.mod present: %+v", plan)
	}
	got := strings.Join(plan.Argv, " ")
	const want = "go test ./cmd/... ./internal/..."
	if got != want {
		t.Errorf("Argv = %q, want %q", got, want)
	}

	makefile, err := os.ReadFile(filepath.Join("..", "..", "templates", "new", "Makefile.tmpl"))
	if err != nil {
		t.Fatalf("reading the generated Makefile template: %v", err)
	}
	if !strings.Contains(string(makefile), "GO_PACKAGES := ./cmd/... ./internal/...") {
		t.Error("the generated Makefile's GO_PACKAGES no longer matches planGoSuite's package list; " +
			"change both together or nise test and make test run different sets")
	}
}

// resolveForTest walks the real registry to the command at path.
func resolveForTest(t *testing.T, path []string) *Command {
	t.Helper()
	res := resolveCommand(Commands(), path)
	if !res.ok || res.leaf == nil {
		t.Fatalf("could not resolve %v in the command registry", path)
	}
	return res.leaf
}
