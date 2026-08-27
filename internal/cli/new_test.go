package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/cli/term"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// newEnv builds an Env for runNew with everything injected: no real
// terminal, no real stdin, no os.Args.
func newEnv(mode output.Mode, interactiveTerminal bool, stdin string, args ...string) (*Env, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Env{
		Out:   output.New(mode, output.Options{}, &buf, &buf),
		Caps:  term.Capabilities{StdoutIsTerminal: interactiveTerminal},
		Args:  args,
		Stdin: strings.NewReader(stdin),
	}, &buf
}

func TestPermuteFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label          string
		args           []string
		wantPositional []string
		wantModules    []recipe.Module
		wantYes        bool
		wantModulePath string
	}{
		{
			label:          "flags before the name",
			args:           []string{"--yes", "--module-path", "example.com/a", "myapp"},
			wantPositional: []string{"myapp"},
			wantYes:        true,
			wantModulePath: "example.com/a",
		},
		{
			// Go's flag package stops at the first non-flag argument, so
			// without permutation this ordering would silently ignore both
			// flags. It is also the ordering a user actually types.
			label:          "flags after the name",
			args:           []string{"myapp", "--yes", "--module-path", "example.com/a"},
			wantPositional: []string{"myapp"},
			wantYes:        true,
			wantModulePath: "example.com/a",
		},
		{
			label:          "flags on both sides",
			args:           []string{"--module", "totp", "myapp", "--yes"},
			wantPositional: []string{"myapp"},
			wantModules:    []recipe.Module{recipe.ModuleTOTP},
			wantYes:        true,
		},
		{
			label:          "repeatable module flag",
			args:           []string{"myapp", "--module", "totp", "--module", "uploads"},
			wantPositional: []string{"myapp"},
			wantModules:    []recipe.Module{recipe.ModuleTOTP, recipe.ModuleUploads},
		},
		{
			label:          "no arguments at all",
			args:           nil,
			wantPositional: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			flags := &newFlags{}
			got, err := permuteFlags(tc.args, flags)
			if err != nil {
				t.Fatalf("permuteFlags(%v) = %v", tc.args, err)
			}
			if !slices.Equal(got, tc.wantPositional) {
				t.Errorf("positional = %v, want %v", got, tc.wantPositional)
			}
			if !slices.Equal(flags.modules.values, tc.wantModules) {
				t.Errorf("modules = %v, want %v", flags.modules.values, tc.wantModules)
			}
			if flags.assumeYes != tc.wantYes {
				t.Errorf("assumeYes = %v, want %v", flags.assumeYes, tc.wantYes)
			}
			if flags.modulePath != tc.wantModulePath {
				t.Errorf("modulePath = %q, want %q", flags.modulePath, tc.wantModulePath)
			}
		})
	}
}

func TestPermuteFlagsRejectsAnUnknownModule(t *testing.T) {
	t.Parallel()

	if _, err := permuteFlags([]string{"myapp", "--module", "nope"}, &newFlags{}); err == nil {
		t.Fatal("permuteFlags with an unknown module = nil, want an error")
	} else if !strings.Contains(err.Error(), "notifications") {
		t.Errorf("error %q does not list the accepted modules", err)
	}
}

func TestNewGeneratesAProject(t *testing.T) {
	t.Chdir(t.TempDir())

	env, out := newEnv(output.ModeHuman, false, "", "myapp", "--module-path", "example.com/myapp", "--yes")
	if err := runNew(env, &newFlags{}); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	for _, want := range []string{
		"nise.json",
		filepath.Join("cmd", "myapp", "main.go"),
		filepath.Join("frontend", "package.json"),
		filepath.Join("internal", "platform", "webui", "webui.go"),
	} {
		if _, err := os.Stat(filepath.Join("myapp", want)); err != nil {
			t.Errorf("expected %s in the generated project: %v", want, err)
		}
	}

	body := out.String()
	for _, want := range []string{"Created myapp", "go mod tidy", "pnpm --dir frontend install"} {
		if !strings.Contains(body, want) {
			t.Errorf("output %q does not mention %q", body, want)
		}
	}
	// The next commands are printed, never run: generation opens no
	// socket and starts no subprocess.
	if _, err := os.Stat(filepath.Join("myapp", "go.sum")); err == nil {
		t.Error("go.sum exists, which means something resolved dependencies during generation")
	}
	if _, err := os.Stat(filepath.Join("myapp", "frontend", "node_modules")); err == nil {
		t.Error("frontend/node_modules exists, which means something installed during generation")
	}
}

func TestNewJSONOutputCarriesTheGeneratedTree(t *testing.T) {
	t.Chdir(t.TempDir())

	env, out := newEnv(output.ModeJSON, true, "", "myapp", "--module-path", "example.com/myapp", "--module", "totp")
	if err := runNew(env, &newFlags{}); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	var got struct {
		Root         string   `json:"root"`
		ModulePath   string   `json:"modulePath"`
		Profile      string   `json:"profile"`
		Modules      []string `json:"modules"`
		Files        []string `json:"files"`
		NextCommands []string `json:"nextCommands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding %q: %v", out.String(), err)
	}
	if got.Root != "myapp" {
		t.Errorf("root = %q, want %q — an absolute path must not reach --json output", got.Root, "myapp")
	}
	if got.Profile != string(recipe.ProfileGoChiPostgresSvelte) {
		t.Errorf("profile = %q", got.Profile)
	}
	if !slices.Equal(got.Modules, []string{"totp"}) {
		t.Errorf("modules = %v, want [totp]", got.Modules)
	}
	if len(got.Files) == 0 || len(got.NextCommands) == 0 {
		t.Errorf("files = %d, nextCommands = %d; both must be reported", len(got.Files), len(got.NextCommands))
	}
	// --json implies non-interactive even though the terminal looks
	// interactive: a prompt would corrupt the document stream.
	if strings.Contains(out.String(), "Project name") {
		t.Error("a prompt reached --json output")
	}
}

func TestNewPromptsOnlyOnAnInteractiveTerminal(t *testing.T) {
	t.Run("interactive: prompts for the name, module path, and modules", func(t *testing.T) {
		t.Chdir(t.TempDir())

		// name, module path, then y/N for each of the four modules in
		// recipe.Modules() order.
		stdin := "prompted\nexample.com/prompted\nn\ny\nn\nn\n"
		env, out := newEnv(output.ModeHuman, true, stdin)
		if err := runNew(env, &newFlags{}); err != nil {
			t.Fatalf("runNew: %v", err)
		}

		if !strings.Contains(out.String(), "Project name") {
			t.Errorf("output %q does not prompt for the name", out.String())
		}
		data, err := os.ReadFile(filepath.Join("prompted", "nise.json"))
		if err != nil {
			t.Fatalf("reading the recipe: %v", err)
		}
		r, err := recipe.Unmarshal(data)
		if err != nil {
			t.Fatalf("parsing the recipe: %v", err)
		}
		if !slices.Equal(r.Modules, []recipe.Module{recipe.ModuleOrganizations}) {
			t.Errorf("modules = %v, want [organizations] (the second module answered y)", r.Modules)
		}
	})

	t.Run("non-interactive: never prompts and never blocks", func(t *testing.T) {
		t.Chdir(t.TempDir())

		// Stdin holds an answer that would be consumed if a prompt ever
		// happened; the run must not touch it.
		env, out := newEnv(output.ModeHuman, false, "wrongname\n", "myapp", "--module-path", "example.com/myapp")
		if err := runNew(env, &newFlags{}); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if strings.Contains(out.String(), "Project name") {
			t.Errorf("output %q prompted on a non-terminal", out.String())
		}
		if _, err := os.Stat("wrongname"); err == nil {
			t.Error("a prompt answer was consumed on a non-interactive run")
		}
	})

	t.Run("--yes on a terminal still never prompts", func(t *testing.T) {
		t.Chdir(t.TempDir())

		env, out := newEnv(output.ModeHuman, true, "wrongname\n", "myapp", "--yes")
		if err := runNew(env, &newFlags{}); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if strings.Contains(out.String(), "Project name") {
			t.Errorf("output %q prompted despite --yes", out.String())
		}
		data, err := os.ReadFile(filepath.Join("myapp", "go.mod"))
		if err != nil {
			t.Fatalf("reading go.mod: %v", err)
		}
		// The module path defaulted to the name rather than being asked for.
		if !strings.Contains(string(data), "module myapp\n") {
			t.Errorf("go.mod = %q, want the module path defaulted to myapp", data)
		}
	})

	t.Run("CI is not interactive even on a terminal", func(t *testing.T) {
		t.Chdir(t.TempDir())

		var buf bytes.Buffer
		env := &Env{
			Out:   output.New(output.ModeHuman, output.Options{}, &buf, &buf),
			Caps:  term.Capabilities{StdoutIsTerminal: true, CI: true},
			Args:  nil,
			Stdin: strings.NewReader("sneaky\n"),
		}
		err := runNew(env, &newFlags{})
		_ = assertCLIError(t, err, clierr.ExitUsage, "new.name_required")
	})
}

func TestNewRefusesANonEmptyTarget(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.MkdirAll(filepath.Join("myapp", "src"), 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	env, _ := newEnv(output.ModeHuman, false, "", "myapp", "--yes")
	err := runNew(env, &newFlags{})
	ce := assertCLIError(t, err, clierr.ExitPrecondition, "new.target_not_empty")
	if !strings.Contains(ce.Cause(), "src/") {
		t.Errorf("cause %q does not name what is in the way", ce.Cause())
	}
	if !strings.Contains(ce.Recovery(), "--force") {
		t.Errorf("recovery %q does not explain that there is no force flag", ce.Recovery())
	}
}

func TestNewRejectsHostileNames(t *testing.T) {
	for _, name := range []string{"..", ".", "con", "nul", "/etc/passwd", "../escape", "my\x00app", "my\napp", "MyApp", "func", "my app"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)

			env, _ := newEnv(output.ModeHuman, false, "", name, "--yes")
			err := runNew(env, &newFlags{})
			_ = assertCLIError(t, err, clierr.ExitUsage, "new.invalid_name")

			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatalf("reading the working directory: %v", readErr)
			}
			if len(entries) != 0 {
				t.Errorf("a rejected name created %d entries", len(entries))
			}
		})
	}
}

func TestNewRejectsAnUnknownProfile(t *testing.T) {
	t.Chdir(t.TempDir())

	env, _ := newEnv(output.ModeHuman, false, "", "myapp", "--yes")
	err := runNew(env, &newFlags{profile: "django-htmx"})
	ce := assertCLIError(t, err, clierr.ExitUsage, "new.invalid_profile")
	if !strings.Contains(ce.Recovery(), string(recipe.ProfileGoChiPostgresSvelte)) {
		t.Errorf("recovery %q does not list the accepted profiles", ce.Recovery())
	}
}

func TestNewRejectsTooManyArguments(t *testing.T) {
	t.Chdir(t.TempDir())

	env, _ := newEnv(output.ModeHuman, false, "", "one", "two", "--yes")
	err := runNew(env, &newFlags{})
	_ = assertCLIError(t, err, clierr.ExitUsage, "usage_error")
}

func TestNewIsRegistered(t *testing.T) {
	t.Parallel()

	if findByName(Commands(), "new") == nil {
		t.Fatal(`Commands() does not include "new"`)
	}
}

// assertCLIError checks that err is a *clierr.Error with the expected exit
// code and machine-readable code, and returns it for further assertions.
func assertCLIError(t *testing.T, err error, want clierr.ExitCode, code string) *clierr.Error {
	t.Helper()

	if err == nil {
		t.Fatal("got nil, want an error")
	}
	ce := clierr.From(err)
	if ce.ExitCode() != want {
		t.Errorf("exit code = %d, want %d (error: %v)", ce.ExitCode(), want, err)
	}
	if ce.Code() != code {
		t.Errorf("error code = %q, want %q", ce.Code(), code)
	}
	if ce.Cause() == "" || ce.Recovery() == "" {
		t.Errorf("error must carry both a cause and a recovery; got %q / %q", ce.Cause(), ce.Recovery())
	}
	return ce
}
