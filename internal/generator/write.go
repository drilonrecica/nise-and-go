package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
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
	// Notes are the caveats that apply to NextCommands. See Notes.
	Notes []string `json:"notes"`
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

	if err := makeDir(root); err != nil {
		return nil, err
	}
	for _, rel := range planDirectories(files) {
		if err := makeDir(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return nil, err
		}
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
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
		// os.WriteFile's mode argument is masked by the process umask, so
		// the file just written is 0o600 under `umask 077` and 0o644 under
		// `umask 022`. That is a generated tree whose contents are
		// identical on two machines but whose modes are not, which the
		// determinism rule does not allow, and which a recursive content
		// diff cannot see. Chmod is not masked, so it is what actually
		// fixes the mode.
		//
		// #nosec G302 -- see the G306 justification above; the two rules
		// object to the same deliberate 0o644.
		if err := os.Chmod(full, FileMode); err != nil {
			return nil, fmt.Errorf("setting the mode of %s: %w", f.Path, err)
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
		Notes:        Notes(),
	}, nil
}

// planDirectories returns every directory the plan needs, relative to the
// project root, sorted. Lexicographic order puts a parent before its own
// children ("internal" sorts before "internal/app"), so creating them in
// this order never needs MkdirAll's implicit parent creation and never
// leaves an intermediate directory whose mode was set by the umask instead
// of by DirMode.
func planDirectories(files []File) []string {
	seen := make(map[string]struct{})
	var dirs []string
	for _, f := range files {
		for dir := path.Dir(f.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := seen[dir]; ok {
				break
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// makeDir creates one directory and forces its mode.
//
// os.MkdirAll applies the process umask to its mode argument, so a
// directory created under `umask 077` is 0o700 rather than DirMode. Chmod
// is not masked. Without the second call, two machines with different
// umasks generate trees that differ in a way no content comparison reveals.
//
// On Windows os.Chmod can only toggle the read-only bit, so the Unix
// permission bits are not meaningful there; that is a property of the
// platform, not a gap in this call.
func makeDir(dir string) error {
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// #nosec G301 -- DirMode is 0o755 deliberately, for the same reason
	// FileMode is 0o644: a generated project's directories must be
	// traversable by every tool the developer runs next and by a container
	// user other than the one that generated the tree.
	if err := os.Chmod(dir, DirMode); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", dir, err)
	}
	return nil
}

// ReplacePathPlaceholder stands in for the path to a local checkout of the
// framework in the pre-release replace directive. It is a literal
// placeholder, never a resolved path: a real absolute path in generated
// output or in generated advice would be machine-specific, which the
// determinism rule forbids.
const ReplacePathPlaceholder = "/path/to/your/nise-and-go"

// NextCommands returns the exact commands to run in a freshly generated
// project, in order. Generation performs none of them itself: it never runs
// go, pnpm, or git, and never opens a network connection, so the user runs
// the network-touching steps knowingly.
//
// The second command is the pre-release replace directive; see Notes for
// why it is there and when it goes away.
func NextCommands(name string) []string {
	return []string{
		"cd " + name,
		"go mod edit -replace " + NiseModulePath + "=" + ReplacePathPlaceholder,
		"go mod tidy",
		"pnpm --dir frontend install",
		"pnpm --dir frontend build",
		"go run ./cmd/" + name,
	}
}

// Notes returns the caveats a user has to know before running
// NextCommands.
//
// There is exactly one, and it is temporary: no tagged release of the
// framework module carries runtime/ yet, so the pinned NiseModuleVersion
// does not resolve from the module proxy and `go mod tidy` fails with
// "unknown revision" until the replace directive is in place. Printing this
// alongside the commands, rather than only in the generated README, is what
// stops a user from meeting that failure with no explanation — a
// known-broken first step presented as a working one is worse than either a
// working step or an honest warning.
func Notes() []string {
	return []string{
		"Pre-release: " + NiseModulePath + " " + NiseModuleVersion + " is not published yet,",
		"so `go mod tidy` cannot resolve it without the replace directive above.",
		"Point it at a local checkout of the framework. Once " + NiseModuleVersion + " is",
		"tagged and on the module proxy, drop that command and delete the replace",
		"line it wrote into go.mod.",
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
