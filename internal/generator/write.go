package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxListedEntries bounds how many blocking entries TargetNotEmptyError
// names. A directory holding a thousand files should produce a readable
// error, not a thousand-line one.
const maxListedEntries = 10

// TargetNotEmptyError reports that the target directory already contains
// something. Generation refuses rather than merging into or overwriting an
// existing tree, and this slice offers no force flag: the safe recovery is
// always a different directory, or a removal the user performs themselves
// and can therefore reason about.
type TargetNotEmptyError struct {
	// Path is the target directory.
	Path string
	// Entries names what is in the way, up to maxListedEntries of them,
	// sorted.
	Entries []string
	// More is how many further entries were not listed.
	More int
}

// Error implements error.
func (e *TargetNotEmptyError) Error() string {
	listed := strings.Join(e.Entries, ", ")
	if e.More > 0 {
		return fmt.Sprintf("%s is not empty: it contains %s, and %d more", e.Path, listed, e.More)
	}
	return fmt.Sprintf("%s is not empty: it contains %s", e.Path, listed)
}

// TargetNotDirectoryError reports that the target path exists and is not a
// directory.
type TargetNotDirectoryError struct {
	// Path is the target path.
	Path string
}

// Error implements error.
func (e *TargetNotDirectoryError) Error() string {
	return fmt.Sprintf("%s exists and is not a directory", e.Path)
}

// Result describes what Write produced. Its fields are the CLI's output in
// both human and JSON mode, so they are all plain data: no absolute path
// beyond the one the user typed, no timestamp, no host detail.
type Result struct {
	// Root is the generated project's directory.
	Root string `json:"root"`
	// Name is the application's name.
	Name string `json:"name"`
	// ModulePath is the generated go.mod's module path.
	ModulePath string `json:"modulePath"`
	// Profile is the generation profile recorded in the recipe.
	Profile string `json:"profile"`
	// Modules are the selected compile-time modules, sorted.
	Modules []string `json:"modules"`
	// Files are the generated files' paths relative to Root, sorted.
	Files []string `json:"files"`
	// NextCommands are the exact commands the user runs next, in order.
	// Generation deliberately runs none of them: it opens no socket and
	// starts no subprocess.
	NextCommands []string `json:"nextCommands"`
}

// Write generates the project described by opts into root.
//
// The whole tree is rendered before anything is created, and root is
// checked for emptiness before the first directory is made, so a template
// failure or a non-empty target leaves the filesystem exactly as it was.
func Write(root string, opts Options) (*Result, error) {
	normalized, err := opts.normalize()
	if err != nil {
		return nil, err
	}

	files, err := Plan(normalized)
	if err != nil {
		return nil, err
	}

	if err := checkTargetEmpty(root); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(root, DirMode); err != nil {
		return nil, fmt.Errorf("creating %s: %w", root, err)
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), DirMode); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(full), err)
		}
		// #nosec G306 -- FileMode is 0o644 deliberately. These are a new
		// project's source files, which every tool the developer runs next
		// (go, pnpm, git, an editor) must be able to read, and which carry
		// no secret: .env.example holds placeholders only, and the recipe
		// holds a profile and version strings. Writing them 0o600 would
		// make a generated project unusable in a container running as a
		// different user than the one that generated it, which is a real
		// deployment shape, in exchange for no confidentiality this tree
		// actually needs.
		if err := os.WriteFile(full, f.Content, FileMode); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.Path, err)
		}
		paths = append(paths, f.Path)
	}

	modules := make([]string, 0, len(normalized.Modules))
	for _, m := range normalized.Modules {
		modules = append(modules, string(m))
	}

	return &Result{
		Root:         root,
		Name:         normalized.Name,
		ModulePath:   normalized.ModulePath,
		Profile:      string(normalized.Profile),
		Modules:      modules,
		Files:        paths,
		NextCommands: NextCommands(normalized.Name),
	}, nil
}

// NextCommands returns the exact commands to run in a freshly generated
// project, in order. Generation performs none of them itself: it never runs
// go, pnpm, or git, and never opens a network connection, so the user runs
// the network-touching steps knowingly.
func NextCommands(name string) []string {
	return []string{
		"cd " + name,
		"go mod tidy",
		"pnpm --dir frontend install",
		"pnpm --dir frontend build",
		"go run ./cmd/" + name,
	}
}

// checkTargetEmpty reports whether root may be generated into. A path that
// does not exist is fine; a file is not a directory; a directory holding
// anything at all — including a dotfile a shell glob would miss — is
// refused.
func checkTargetEmpty(root string) error {
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking %s: %w", root, err)
	}
	if !info.IsDir() {
		return &TargetNotDirectoryError{Path: root}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("reading %s: %w", root, err)
	}
	if len(entries) == 0 {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)

	listed := names
	more := 0
	if len(listed) > maxListedEntries {
		more = len(listed) - maxListedEntries
		listed = listed[:maxListedEntries]
	}
	return &TargetNotEmptyError{Path: root, Entries: listed, More: more}
}
