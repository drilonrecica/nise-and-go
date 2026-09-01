package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// Apply performs the upgrade and returns what it did.
//
// It re-plans rather than taking a Report, so what is written is computed
// against the tree as it is at the moment of writing — a Report computed
// minutes ago against a tree that has since changed would apply the wrong
// thing while reporting the right one. The returned Report is the one that was
// applied.
//
// Every write goes through an os.Root opened on the project, so a path can
// only ever resolve inside it. That is not defence against an attacker — the
// paths come from this repository's own generator — it is defence against the
// thing that actually happens: a symlink somebody put inside their project, a
// path assembled wrongly by a future change here, and a command that writes
// into a developer's working tree having no business being able to reach
// outside it.
//
// The write order matters and is not arbitrary:
//
//  1. Nise-owned files, which are the framework's to rewrite.
//  2. Codemods over application-owned Go source, so a rewritten import lands
//     in a tree whose generated half is already the new one.
//  3. Dependency manifests, which are what a subsequent `go mod tidy` reads.
//  4. The recipe, last. It is the record that the upgrade happened, and
//     writing it first would leave a project claiming to be upgraded after a
//     failure halfway through.
func Apply(opts Options) (Report, error) {
	report, err := Plan(opts)
	if err != nil {
		return Report{}, err
	}
	if report.UpToDate {
		return report, nil
	}

	current, err := readRecipe(opts.Root)
	if err != nil {
		return Report{}, err
	}
	target := current
	target.CLIVersion = opts.CLIVersion
	target.RuntimeVersion = opts.RuntimeVersion
	if target.RuntimeVersion == "" {
		target.RuntimeVersion = generator.NiseModuleVersion
	}

	planned, err := planFiles(opts.Root, target)
	if err != nil {
		return Report{}, err
	}
	byPath := make(map[string]generator.File, len(planned))
	for _, file := range planned {
		byPath[file.Path] = file
	}

	root, err := os.OpenRoot(opts.Root)
	if err != nil {
		return Report{}, fmt.Errorf("opening the project: %w", err)
	}
	defer func() { _ = root.Close() }()

	if err := writeFiles(root, report.Files, byPath); err != nil {
		return Report{}, err
	}
	if opts.Codemods {
		chain, _ := path(current.RuntimeVersion, target.RuntimeVersion, migrationTable(opts))
		if err := applyCodemods(root, planned, chain); err != nil {
			return Report{}, err
		}
	}
	if err := applyDependencies(root); err != nil {
		return Report{}, err
	}
	if err := writeRecipe(root, target); err != nil {
		return Report{}, err
	}
	return report, nil
}

// projectFileMode is the permission every file an upgrade writes gets.
//
// 0644 rather than 0600: a generated project's source is ordinary source, and
// a file only its owner can read breaks a container build that copies the tree
// and runs as another user.
const projectFileMode = 0o644

func writeFiles(root *os.Root, changes []FileChange, planned map[string]generator.File) error {
	for _, change := range changes {
		if change.Action == ActionRemove {
			if err := root.Remove(change.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removing %s: %w", change.Path, err)
			}
			continue
		}
		file, ok := planned[change.Path]
		if !ok {
			// Cannot happen: every create and update comes from the plan this
			// map was built from. Reported rather than ignored, because a
			// silent skip here is a file the report claims was written.
			return fmt.Errorf("internal: %s was planned for %s and is not in the plan", change.Path, change.Action)
		}
		if directory := pathDir(change.Path); directory != "" {
			if err := root.MkdirAll(directory, 0o750); err != nil {
				return fmt.Errorf("creating the directory for %s: %w", change.Path, err)
			}
		}
		if err := root.WriteFile(change.Path, file.Content, projectFileMode); err != nil {
			return fmt.Errorf("writing %s: %w", change.Path, err)
		}
	}
	return nil
}

// applyCodemods rewrites application-owned Go source, and only where a
// declared codemod matched.
//
// A file whose content did not change is not written at all. That is not an
// optimization: rewriting a file with identical bytes still changes its
// modification time, which is enough to make a build system rebuild it and a
// developer wonder what happened to it.
func applyCodemods(root *os.Root, planned []generator.File, chain []Migration) error {
	for _, file := range planned {
		if file.Owner != generator.OwnerApp || !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		source, err := root.ReadFile(file.Path)
		if errors.Is(err, os.ErrNotExist) {
			// The application deleted it, which is its prerogative.
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", file.Path, err)
		}

		text := string(source)
		changed := false
		for _, migration := range chain {
			for _, mod := range migration.Codemods {
				rewritten, count := mod.apply(text)
				if count > 0 {
					text, changed = rewritten, true
				}
			}
		}
		if !changed {
			continue
		}
		if err := root.WriteFile(file.Path, []byte(text), projectFileMode); err != nil {
			return fmt.Errorf("writing %s: %w", file.Path, err)
		}
	}
	return nil
}

func applyDependencies(root *os.Root) error {
	frontendPins := append(generator.FrontendRuntimeDependencies(), generator.FrontendDependencies()...)

	for _, manifest := range []struct {
		name string
		pins []generator.Dependency
		bump func(string, []generator.Dependency) (string, []RequireChange)
	}{
		{goModFileName, generator.GoDependencies(), bumpGoMod},
		{packageJSONFileName, frontendPins, bumpPackageJSON},
	} {
		source, err := root.ReadFile(manifest.name)
		if errors.Is(err, os.ErrNotExist) {
			// The application deleted it, which is its prerogative — the
			// frontend is app-owned in its entirety.
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", manifest.name, err)
		}
		bumped, changes := manifest.bump(string(source), manifest.pins)
		if len(changes) == 0 {
			continue
		}
		if err := root.WriteFile(manifest.name, []byte(bumped), projectFileMode); err != nil {
			return fmt.Errorf("writing %s: %w", manifest.name, err)
		}
	}
	return nil
}

// writeRecipe records that the upgrade happened, through the recipe package's
// own canonicalizer so an upgraded project's recipe is byte-identical to one
// `nise new` would write with the same values.
func writeRecipe(root *os.Root, r recipe.Recipe) error {
	encoded, err := recipe.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", recipe.FileName, err)
	}
	if err := root.WriteFile(recipe.FileName, encoded, projectFileMode); err != nil {
		return fmt.Errorf("writing %s: %w", recipe.FileName, err)
	}
	return nil
}

// pathDir returns the directory part of a slash-separated project path, or ""
// when the file sits at the project root.
func pathDir(p string) string {
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(p)))
	if directory == "." || directory == "/" {
		return ""
	}
	return directory
}
