package reference_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

func referenceDir(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{repoRoot(t), "examples", "reference"}, parts...)...)
}

// referenceRecipe is examples/reference/recipe.json: the exact `nise new`
// arguments the reference application is built with.
type referenceRecipe struct {
	Name       string   `json:"name"`
	ModulePath string   `json:"modulePath"`
	Profile    string   `json:"profile"`
	Modules    []string `json:"modules"`
}

func readRecipe(t *testing.T) referenceRecipe {
	t.Helper()
	data, err := os.ReadFile(referenceDir(t, "recipe.json")) // #nosec G304 -- a fixed path in this repository.
	if err != nil {
		t.Fatalf("reading recipe.json: %v", err)
	}
	var parsed referenceRecipe
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parsing recipe.json: %v", err)
	}
	return parsed
}

// plan renders the project the reference recipe describes, so the overlay can
// be compared against what the generator actually owns.
func plan(t *testing.T) map[string]generator.Ownership {
	t.Helper()
	declared := readRecipe(t)

	modules := make([]recipe.Module, 0, len(declared.Modules))
	for _, name := range declared.Modules {
		modules = append(modules, recipe.Module(name))
	}
	files, err := generator.Plan(generator.Options{
		Name:       declared.Name,
		ModulePath: declared.ModulePath,
		Profile:    recipe.Profile(declared.Profile),
		Modules:    modules,
		CLIVersion: "v0.0.0-reference",
	})
	if err != nil {
		t.Fatalf("planning the reference project: %v", err)
	}
	owners := make(map[string]generator.Ownership, len(files))
	for _, file := range files {
		owners[file.Path] = file.Owner
	}
	return owners
}

// overlayFiles lists the overlay, relative and slash-separated.
func overlayFiles(t *testing.T) []string {
	t.Helper()
	root := referenceDir(t, "app")
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the overlay: %v", err)
	}
	sort.Strings(paths)
	return paths
}

// The invariant the overlay design lives or dies by.
//
// A Nise-owned file in the overlay would mean the reference application works
// by overwriting generated output — the one thing ADR 0003 says an application
// never needs to do. If the reference application needed it, that would be
// evidence against the framework, and it would be evidence that arrived
// silently.
func TestTheOverlayOwnsNothingTheGeneratorOwns(t *testing.T) {
	t.Parallel()

	owners := plan(t)
	files := overlayFiles(t)
	if len(files) == 0 {
		t.Fatal("the overlay is empty, so this test proves nothing")
	}

	for _, path := range files {
		owner, generated := owners[path]
		if !generated {
			// A file the generator does not write at all: an ordinary new
			// application file.
			continue
		}
		if owner == generator.OwnerNise {
			t.Errorf("examples/reference/app/%s overwrites a file nise owns; "+
				"the reference application must not need to do that (ADR 0003)", path)
		}
	}
}

// Every overlay file that replaces a generated one keeps the app-owned header,
// so `nise check`'s ownership invariant still passes on the built application.
//
// The rule is asymmetric on purpose, and this is the half that applies here:
// an app-owned file may be rewritten freely, but it must not carry a
// *generated-code* marker — that is how nise detects generated output having
// landed on top of a file the application owns.
func TestOverlayFilesDoNotClaimToBeGenerated(t *testing.T) {
	t.Parallel()

	for _, path := range overlayFiles(t) {
		data, err := os.ReadFile(referenceDir(t, "app", filepath.FromSlash(path))) // #nosec G304 -- a path from this repository's own overlay.
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(data), generator.NiseOwnedHeader) {
			t.Errorf("examples/reference/app/%s carries the Nise-owned header, "+
				"which would fail the built application's own `nise check`", path)
		}
	}
}

// The recipe has to be one `nise new` would accept. A reference application
// nobody can build is a specification with a directory.
func TestTheRecipeIsOneNiseWouldAccept(t *testing.T) {
	t.Parallel()
	declared := readRecipe(t)

	if err := generator.ValidateName(declared.Name); err != nil {
		t.Errorf("recipe.json names the project %q, which nise new would refuse: %v", declared.Name, err)
	}
	if err := generator.ValidateModulePath(declared.ModulePath); err != nil {
		t.Errorf("recipe.json declares module path %q, which nise new would refuse: %v", declared.ModulePath, err)
	}

	known := map[recipe.Module]bool{}
	for _, module := range recipe.Modules() {
		known[module] = true
	}
	for _, name := range declared.Modules {
		if !known[recipe.Module(name)] {
			t.Errorf("recipe.json selects the module %q, which nise does not have", name)
		}
	}
	// The reference application exists to exercise the whole surface. A module
	// left unselected is a module nothing proves end to end.
	if len(declared.Modules) != len(known) {
		var missing []string
		selected := map[string]bool{}
		for _, name := range declared.Modules {
			selected[name] = true
		}
		for module := range known {
			if !selected[string(module)] {
				missing = append(missing, string(module))
			}
		}
		sort.Strings(missing)
		t.Errorf("the reference application does not select %v; every optional module should be exercised by it", missing)
	}
}

// The build script is what turns the overlay into an application, and the
// specification tells a reader to run it.
func TestTheBuildScriptIsRunnable(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(referenceDir(t, "build.sh"))
	if err != nil {
		t.Fatalf("examples/reference/build.sh: %v", err)
	}
	// Windows has no execute bit. Go synthesizes a file's mode there from the
	// read-only attribute, so Perm()&0o111 is 0 for every checked-out file and
	// this assertion would report a missing mode bit that the platform does
	// not have to give. The bit is a real property of the committed file --
	// git tracks it, and the Linux and macOS legs check it -- so guard the
	// assertion rather than dropping it.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Error("build.sh is not executable, and the specification tells a reader to run it directly")
	}

	script, err := os.ReadFile(referenceDir(t, "build.sh")) // #nosec G304 -- a fixed path in this repository.
	if err != nil {
		t.Fatalf("reading build.sh: %v", err)
	}
	source := string(script)

	// Overwriting a directory somebody named is a different and much worse
	// command than creating one.
	if !strings.Contains(source, "already exists") {
		t.Error("build.sh does not refuse an existing destination")
	}
	// A reference application built by a different nise than the one being
	// changed proves nothing about the change.
	if !strings.Contains(source, "go build -o \"$nise\" ./cmd/nise") {
		t.Error("build.sh does not build nise from this checkout, so it could test whatever is on PATH instead")
	}
	if !strings.Contains(source, "set -eu") {
		t.Error("build.sh does not exit on error, so a failed generation would still copy the overlay over nothing")
	}
}
