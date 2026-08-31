package feature

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// ErrExists reports that the plan would have written over a file that is
// already there.
var ErrExists = errors.New("the feature already exists")

// ErrNotAProject reports that the directory the command was run in is not a
// Go project. It is its own error because the recovery is not "retry": it is
// "you are in the wrong directory".
var ErrNotAProject = errors.New("no go.mod here, so this is not the root of a Go project")

// ExistsError names every path a plan collided with.
//
// It names all of them rather than the first, because a person who ran the
// command in the wrong project, or twice, wants to know what is there — and a
// message that stops at the first path invites them to delete that one file
// and run again into the second.
type ExistsError struct {
	// Paths are the existing paths, relative to the project root, sorted.
	Paths []string
}

// Error implements error.
func (e *ExistsError) Error() string {
	return fmt.Sprintf("%s: %s already exist(s)", ErrExists, strings.Join(e.Paths, ", "))
}

// Is makes errors.Is(err, ErrExists) true.
func (e *ExistsError) Is(target error) bool { return target == ErrExists }

// Write writes a plan into the project at root.
//
// It creates files and modifies none, and it is transactional against its own
// precondition: every path is checked before anything is written, so a plan
// that collides leaves the project exactly as it found it. A generator that
// wrote three files and then stopped at the fourth would leave a half-feature
// somebody has to identify and remove by hand.
//
// It never removes or truncates. The worst thing a wrong run can do is create
// files you delete.
func Write(root string, plan Plan) ([]string, error) {
	existing := make([]string, 0, len(plan.Files))
	for _, file := range plan.Files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if _, err := os.Stat(target); err == nil {
			existing = append(existing, file.Path)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("checking %s: %w", file.Path, err)
		}
	}
	if len(existing) > 0 {
		slices.Sort(existing)
		return nil, &ExistsError{Paths: existing}
	}

	written := make([]string, 0, len(plan.Files))
	for _, file := range plan.Files {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), generator.DirMode); err != nil {
			return written, fmt.Errorf("creating the directory for %s: %w", file.Path, err)
		}
		// O_EXCL rather than a plain create: the stat above is a courtesy that
		// gives a good message, and this is the check that actually holds. A
		// stat-then-create has a window; a file created in that window would
		// otherwise be silently overwritten, which is the one thing this
		// generator promises never to do.
		handle, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, generator.FileMode) // #nosec G304 -- root joined with a path this package rendered from a validated name.
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return written, &ExistsError{Paths: []string{file.Path}}
			}
			return written, fmt.Errorf("creating %s: %w", file.Path, err)
		}
		_, writeErr := handle.Write(file.Content)
		closeErr := handle.Close()
		if writeErr != nil {
			return written, fmt.Errorf("writing %s: %w", file.Path, writeErr)
		}
		if closeErr != nil {
			return written, fmt.Errorf("closing %s: %w", file.Path, closeErr)
		}
		written = append(written, file.Path)
	}
	return written, nil
}

// ModulePathOf reads the module path out of a project's go.mod.
//
// It is read rather than asked for, so a generated import cannot disagree with
// the module it is generated into.
func ModulePathOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- root joined with this package's own constant.
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotAProject, err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("%w: its go.mod declares no module path", ErrNotAProject)
}

// NextMigrationVersion is the version a new migration is written as: one past
// the highest already in db/migrations.
//
// The history has to be contiguous — the runtime's compatibility check refuses
// a gap, and a gap is how a missing migration hides — so this reads what is
// there rather than deriving a number from the clock. Timestamped filenames
// would sidestep the read and reintroduce exactly the nondeterminism ADR 0002
// forbids.
func NextMigrationVersion(root string) (int, error) {
	dir := filepath.Join(root, "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("%w: reading db/migrations: %w", ErrNotAProject, err)
	}
	highest := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			continue
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			// A file that is not NNNNN_name.sql is not part of the history and
			// is not this function's to complain about; the migration runner
			// is what refuses one.
			continue
		}
		if version > highest {
			highest = version
		}
	}
	if highest == 0 {
		return 0, fmt.Errorf("%w: db/migrations holds no numbered migration", ErrNotAProject)
	}
	return highest + 1, nil
}
