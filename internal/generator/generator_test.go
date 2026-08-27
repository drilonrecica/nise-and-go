package generator_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
	"github.com/drilonrecica/nise-and-go/templates"
)

// updateGolden regenerates the committed golden manifests. Run it with
// `go test ./internal/generator/... -update` after a deliberate change to a
// template, and review the diff: the manifest is the record of exactly what
// a generated project contains.
var updateGolden = flag.Bool("update", false, "rewrite the golden manifests in testdata")

// fixedVersion is the version every test pins. The recipe records the
// generating CLI's version, which changes on every release, so a golden
// test must inject one rather than read the build's own (ADR 0010).
const fixedVersion = "v0.0.0-test"

func defaultOptions() generator.Options {
	return generator.Options{
		Name:       "myapp",
		ModulePath: "github.com/example/myapp",
		CLIVersion: fixedVersion,
	}
}

func allModulesOptions() generator.Options {
	opts := defaultOptions()
	opts.Modules = recipe.Modules()
	return opts
}

// TestPlanIsDeterministic is the hard gate: two independent runs with equal
// options must produce byte-identical trees. It compares a recursive hash
// of two separately written trees rather than the in-memory plan, so the
// path separators, the directory creation order, and the write itself are
// all covered.
func TestGenerationIsDeterministic(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts generator.Options
	}{
		{"default recipe", defaultOptions()},
		{"every module selected", allModulesOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first := filepath.Join(t.TempDir(), "myapp")
			second := filepath.Join(t.TempDir(), "myapp")

			if _, err := generator.Write(first, tc.opts); err != nil {
				t.Fatalf("first Write: %v", err)
			}
			if _, err := generator.Write(second, tc.opts); err != nil {
				t.Fatalf("second Write: %v", err)
			}

			a := treeManifest(t, first)
			b := treeManifest(t, second)
			if a != b {
				t.Errorf("two runs produced different trees:\n--- first ---\n%s\n--- second ---\n%s", a, b)
			}
		})
	}
}

// TestGeneratedTreeMatchesGolden pins the exact set of paths and the exact
// bytes of every file, for the default recipe and for a recipe with every
// module selected. A template change that was not intended shows up here as
// a diff before it reaches a user's project.
func TestGeneratedTreeMatchesGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		golden string
		opts   generator.Options
	}{
		{"default", "default.txt", defaultOptions()},
		{"all modules", "all-modules.txt", allModulesOptions()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), tc.opts.Name)
			if _, err := generator.Write(root, tc.opts); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := treeManifest(t, root)

			goldenPath := filepath.Join("testdata", "golden", tc.golden)
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
					t.Fatalf("creating testdata: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("writing golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("reading golden (run with -update to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("generated tree does not match %s.\n--- got ---\n%s\n--- want ---\n%s\nRun `go test ./internal/generator/... -update` and review the diff if the change was intended.",
					goldenPath, got, want)
			}
		})
	}
}

// TestEveryTemplateIsInTheManifest is what makes "every template is
// exercised by a test" structural rather than a promise: a template file
// added to templates/new without a manifest entry would otherwise be
// silently skipped by every generation and every golden test.
func TestEveryTemplateIsInTheManifest(t *testing.T) {
	t.Parallel()

	var onDisk []string
	err := fs.WalkDir(templates.FS, "new", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		onDisk = append(onDisk, strings.TrimPrefix(p, "new/"))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded templates: %v", err)
	}
	sort.Strings(onDisk)

	inManifest := generator.TemplatePaths()
	sort.Strings(inManifest)

	if !slices.Equal(onDisk, inManifest) {
		t.Errorf("the embedded templates and the manifest disagree.\nembedded: %v\nmanifest: %v", onDisk, inManifest)
	}
	if len(onDisk) == 0 {
		t.Fatal("no templates are embedded at all")
	}
}

// TestEveryGeneratedFileDeclaresItsOwnership checks the requirement that
// makes regeneration safe: a reader, and a later nise generate, must be
// able to tell from the file itself whether editing it is pointless.
func TestEveryGeneratedFileDeclaresItsOwnership(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// JSON has no comment syntax. ADR 0009 resolves that by declaring
	// those files' ownership by path instead, so they are exempt here and
	// nowhere else.
	jsonExempt := map[string]bool{
		"nise.json":               true,
		".nise/architecture.json": true,
		"frontend/package.json":   true,
		"frontend/tsconfig.json":  true,
	}

	for _, f := range files {
		if jsonExempt[f.Path] {
			continue
		}
		want := generator.AppOwnedHeader
		if f.Owner == generator.OwnerNise {
			want = generator.NiseOwnedHeader
		}
		head := string(f.Content)
		if len(head) > 400 {
			head = head[:400]
		}
		if !strings.Contains(head, want) {
			t.Errorf("%s is %s-owned but its first 400 bytes do not carry %q", f.Path, f.Owner, want)
		}
	}
}

// TestGeneratedFilesUseLFOnly guards the line-ending rule. A CRLF that
// slipped in through a Windows checkout of this repository would make every
// generated project on that machine differ from every other machine's.
func TestGeneratedFilesUseLFOnly(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Content), "\r") {
			t.Errorf("%s contains a carriage return", f.Path)
		}
		if !strings.HasSuffix(string(f.Content), "\n") {
			t.Errorf("%s does not end with a newline", f.Path)
		}
	}
}

// TestPlanContainsNoNondeterministicMarkers is a direct check on the
// determinism rule's named prohibitions, catching a template that
// interpolated something environment-derived.
func TestPlanContainsNoNondeterministicMarkers(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Absolute paths are the concrete leak this catches: a template that
	// interpolated a working directory or a home directory would generate
	// a project that only builds on the machine that made it. The
	// username is deliberately not checked, because it collides with the
	// framework's own module path (github.com/<owner>/...) on the
	// maintainer's machine and would be a permanent false positive.
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	forbidden := []string{cwd, `C:\Users\`}
	if home != "" && home != "/" {
		forbidden = append(forbidden, home)
	}

	// A timestamp is the other named prohibition, and the one most likely
	// to arrive by accident through a build stamp.
	timestamp := regexp.MustCompile(`\b(19|20)\d\d-\d\d-\d\dT\d\d:\d\d:\d\d`)

	for _, f := range files {
		body := string(f.Content)
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("%s contains the environment-derived string %q", f.Path, needle)
			}
		}
		if m := timestamp.FindString(body); m != "" && !strings.Contains(f.Path, "README") {
			t.Errorf("%s contains what looks like a timestamp: %q", f.Path, m)
		}
	}
}

// TestWriteRefusesNonEmptyTarget is the refuse-to-destroy rule. There is no
// force flag in this slice, so the error has to name what is in the way.
func TestWriteRefusesNonEmptyTarget(t *testing.T) {
	t.Parallel()

	t.Run("directory holding a file", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "myapp")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("setup: %v", err)
		}
		existing := filepath.Join(root, "important.txt")
		if err := os.WriteFile(existing, []byte("do not lose me"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := generator.Write(root, defaultOptions())
		var notEmpty *generator.TargetNotEmptyError
		if !errors.As(err, &notEmpty) {
			t.Fatalf("Write = %v, want *TargetNotEmptyError", err)
		}
		if !slices.Contains(notEmpty.Entries, "important.txt") {
			t.Errorf("Entries = %v, want it to name important.txt", notEmpty.Entries)
		}

		got, readErr := os.ReadFile(existing)
		if readErr != nil || string(got) != "do not lose me" {
			t.Errorf("the existing file was modified: %q, %v", got, readErr)
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 1 {
			t.Errorf("Write created %d entries in a target it refused", len(entries)-1)
		}
	})

	t.Run("directory holding only a dotfile", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "myapp")
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_, err := generator.Write(root, defaultOptions())
		var notEmpty *generator.TargetNotEmptyError
		if !errors.As(err, &notEmpty) {
			t.Fatalf("Write = %v, want *TargetNotEmptyError", err)
		}
		if !slices.Contains(notEmpty.Entries, ".git/") {
			t.Errorf("Entries = %v, want it to name .git/", notEmpty.Entries)
		}
	})

	t.Run("target is a file", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "myapp")
		if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_, err := generator.Write(root, defaultOptions())
		var notDir *generator.TargetNotDirectoryError
		if !errors.As(err, &notDir) {
			t.Fatalf("Write = %v, want *TargetNotDirectoryError", err)
		}
	})

	t.Run("empty directory is accepted", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "myapp")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if _, err := generator.Write(root, defaultOptions()); err != nil {
			t.Fatalf("Write into an empty directory: %v", err)
		}
	})
}

// TestWriteRejectsHostileNames checks that a hostile name is refused before
// any filesystem call, so nothing is created anywhere.
func TestWriteRejectsHostileNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "..", ".", "con", "nul", "/etc/passwd", "../escape", "my\x00app", "my\napp", "MyApp", "func"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			opts := defaultOptions()
			opts.Name = name
			opts.ModulePath = ""

			_, err := generator.Write(filepath.Join(parent, "target"), opts)
			var nameErr *generator.NameError
			if !errors.As(err, &nameErr) {
				t.Fatalf("Write with name %q = %v, want *NameError", name, err)
			}

			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatalf("reading the parent directory: %v", readErr)
			}
			if len(entries) != 0 {
				t.Errorf("a rejected name still created %d entries", len(entries))
			}
		})
	}
}

// TestPlanRejectsAnInvalidModulePath covers the second name surface.
func TestPlanRejectsAnInvalidModulePath(t *testing.T) {
	t.Parallel()

	opts := defaultOptions()
	opts.ModulePath = "github.com/../evil"
	_, err := generator.Plan(opts)
	var nameErr *generator.NameError
	if !errors.As(err, &nameErr) {
		t.Fatalf("Plan = %v, want *NameError", err)
	}
}

// TestPlanRejectsAnInvalidRecipe proves the recipe's own validation is not
// bypassed: a version carrying a space (the rendered-banner mistake ADR
// 0010 exists to stop) must fail generation, not be written.
func TestPlanRejectsAnInvalidRecipe(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ label, version string }{
		{"empty", ""},
		{"rendered banner", "nise v1.2.3 (commit abcd, built 2026-08-27T00:00:00Z)"},
		{"trailing space", "v1.2.3 "},
	} {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			opts := defaultOptions()
			opts.CLIVersion = tc.version
			if _, err := generator.Plan(opts); err == nil {
				t.Fatalf("Plan with CLIVersion %q = nil, want an error", tc.version)
			}
		})
	}
}

// TestModulesAreSortedAndDeduplicated pins the canonicalization the recipe
// format requires, from an unsorted, duplicated input.
func TestModulesAreSortedAndDeduplicated(t *testing.T) {
	t.Parallel()

	opts := defaultOptions()
	opts.Modules = []recipe.Module{
		recipe.ModuleUploads,
		recipe.ModuleOrganizations,
		recipe.ModuleUploads,
		recipe.ModuleTOTP,
	}

	r, err := opts.Recipe()
	if err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	want := []recipe.Module{recipe.ModuleOrganizations, recipe.ModuleTOTP, recipe.ModuleUploads}
	if !slices.Equal(r.Modules, want) {
		t.Errorf("Modules = %v, want %v", r.Modules, want)
	}
}

// TestFileModesAreExplicit checks that generated files and directories
// carry exactly the fixed modes.
//
// The comparison is equality, not a subset check. An earlier version of
// this test reasoned that "a restrictive umask can only clear bits" and
// asserted got&^want == 0, which passes on 0o600 under `umask 077` — the
// precise behavior the determinism rule forbids, since two machines with
// different umasks would then produce trees that differ in a way no
// content comparison reveals. A test that accommodates the defect it
// guards is worse than no test.
//
// TestFileModesAreIndependentOfUmask below drives the same assertion under
// a deliberately hostile umask.
func TestFileModesAreExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod only toggles the read-only bit on Windows, so Unix permission bits are not meaningful there")
	}
	t.Parallel()

	root := filepath.Join(t.TempDir(), "myapp")
	if _, err := generator.Write(root, defaultOptions()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertExactModes(t, root)
}

// TestFileModesAreIndependentOfUmask is the regression test for the umask
// dependency itself: it sets a maximally restrictive umask for the
// duration of the generation and requires the modes to come out unchanged.
//
// It cannot run in parallel with anything, because the umask is
// process-wide state, so it does not call t.Parallel and restores the
// previous value before returning.
func TestFileModesAreIndependentOfUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix umask has no equivalent on Windows")
	}

	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	root := filepath.Join(t.TempDir(), "myapp")
	if _, err := generator.Write(root, defaultOptions()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertExactModes(t, root)
}

// assertExactModes requires every file under root to be exactly FileMode
// and every directory, root included, to be exactly DirMode.
func assertExactModes(t *testing.T, root string) {
	t.Helper()

	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := generator.FileMode
		if d.IsDir() {
			want = generator.DirMode
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %04o, want exactly %04o", p, got, want)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatalf("walking the generated tree: %v", err)
	}
	if checked == 0 {
		t.Fatal("walked no entries at all")
	}
}

// TestNextCommandsCarryTheReplaceCaveat pins the disclosure of the one
// known-broken first step. The pinned framework version is not published,
// so `go mod tidy` fails without a replace directive; a user who is told to
// run tidy and nothing else meets an "unknown revision" error with no
// explanation.
func TestNextCommandsCarryTheReplaceCaveat(t *testing.T) {
	t.Parallel()

	commands := generator.NextCommands("myapp")
	joined := strings.Join(commands, "\n")

	if !strings.Contains(joined, "go mod edit -replace "+generator.NiseModulePath+"=") {
		t.Errorf("NextCommands does not include the replace directive:\n%s", joined)
	}
	replaceAt, tidyAt := -1, -1
	for i, c := range commands {
		if strings.Contains(c, "-replace") {
			replaceAt = i
		}
		if strings.Contains(c, "go mod tidy") {
			tidyAt = i
		}
	}
	if replaceAt < 0 || tidyAt < 0 || replaceAt > tidyAt {
		t.Errorf("the replace directive must come before `go mod tidy`; got replace=%d tidy=%d", replaceAt, tidyAt)
	}

	notes := strings.Join(generator.Notes(), " ")
	for _, want := range []string{generator.NiseModuleVersion, "not published", "drop"} {
		if !strings.Contains(strings.ToLower(notes), strings.ToLower(want)) {
			t.Errorf("Notes does not mention %q:\n%s", want, notes)
		}
	}
}

// TestReplacePlaceholderIsNotARealPath guards the determinism rule against
// the obvious mistake in the caveat above: resolving the framework checkout
// to a real absolute path would be machine-specific advice baked into
// generated output.
func TestReplacePlaceholderIsNotARealPath(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(generator.ReplacePathPlaceholder); err == nil {
		t.Errorf("ReplacePathPlaceholder %q exists on this machine, so it is a real path and not a placeholder", generator.ReplacePathPlaceholder)
	}

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var readme string
	for _, f := range files {
		if f.Path == "README.md" {
			readme = string(f.Content)
		}
	}
	if readme == "" {
		t.Fatal("no README.md in the plan")
	}
	if !strings.Contains(readme, generator.ReplacePathPlaceholder) {
		t.Error("the generated README does not carry the replace placeholder")
	}
	if !strings.Contains(readme, "not published yet") {
		t.Error("the generated README does not say the pinned framework version is unpublished")
	}
}

// TestRecipeRuntimeVersionMatchesGoMod is the invariant ADR 0010 defines:
// runtimeVersion is the version of runtime/ the project targets, which is
// what go.mod requires — not the version of the binary that generated the
// project. nise doctor and a future nise upgrade compare the two.
func TestRecipeRuntimeVersionMatchesGoMod(t *testing.T) {
	t.Parallel()

	opts := defaultOptions()
	opts.CLIVersion = "v0.0.0-20260827195219-237478b37291+dirty"

	r, err := opts.Recipe()
	if err != nil {
		t.Fatalf("Recipe: %v", err)
	}
	if r.RuntimeVersion != generator.NiseModuleVersion {
		t.Errorf("runtimeVersion = %q, want %q (the version go.mod requires)", r.RuntimeVersion, generator.NiseModuleVersion)
	}
	if r.CLIVersion != opts.CLIVersion {
		t.Errorf("cliVersion = %q, want the generating binary's own version %q", r.CLIVersion, opts.CLIVersion)
	}

	files, err := generator.Plan(opts)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, f := range files {
		if f.Path != "go.mod" {
			continue
		}
		want := generator.NiseModulePath + " " + r.RuntimeVersion
		if !strings.Contains(string(f.Content), want) {
			t.Errorf("go.mod does not require %q:\n%s", want, f.Content)
		}
		return
	}
	t.Fatal("no go.mod in the plan")
}

// TestDockerignoreKeepsTheBuildContextClean checks the entries that matter:
// the Dockerfile's COPY . . would otherwise pull a host node_modules and a
// stale local frontend build into the image, and must still carry the
// committed embed placeholder.
func TestDockerignoreKeepsTheBuildContextClean(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var body string
	for _, f := range files {
		if f.Path == ".dockerignore" {
			body = string(f.Content)
		}
	}
	if body == "" {
		t.Fatal("no .dockerignore in the plan")
	}

	for _, want := range []string{
		"frontend/node_modules/",
		"frontend/.svelte-kit/",
		"internal/platform/webui/embedded/client/",
		".git/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf(".dockerignore does not exclude %q", want)
		}
	}
	// Excluding the placeholder would break the Go build stage, which
	// compiles before the frontend output is copied in.
	if strings.Contains(body, "placeholder.html") {
		t.Error(".dockerignore excludes the committed embed placeholder, which the Go build stage needs")
	}
}

// treeManifest returns a stable, sorted "sha256  path" listing of every
// file under root, with paths relative to root and slash-separated. It is
// the comparison unit for both the determinism test and the golden test.
func treeManifest(t *testing.T, root string) string {
	t.Helper()

	// Paths are collected during the walk and read afterwards, so no
	// filesystem operation happens inside the walk callback.
	var files []string
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	lines := make([]string, 0, len(files))
	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("relativizing %s: %v", p, err)
		}
		data, err := os.ReadFile(p) // #nosec G304 -- p comes from walking a directory this test created.
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		sum := sha256.Sum256(data)
		lines = append(lines, fmt.Sprintf("%s  %s", hex.EncodeToString(sum[:]), path.Clean(filepath.ToSlash(rel))))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
