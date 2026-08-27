package agentsfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// AgentsFileName is AGENTS.md's name at the project root.
const AgentsFileName = "AGENTS.md"

// ArchitectureDir and ArchitectureFileName together locate the
// machine-readable architecture map, at
// <project root>/.nise/architecture.json. It lives under a dot-directory
// rather than at the project root: docs/generated-application-layout.md's
// documented root-level tree names AGENTS.md and nise.json explicitly and
// does not reserve a slot for a machine-only artifact, and a dot-prefixed
// "tool metadata" directory (the same idiom .git/, .vscode/, and .idea/
// use) keeps this addition out of that documented root without requiring
// a change to it.
const (
	ArchitectureDir      = ".nise"
	ArchitectureFileName = "architecture.json"
)

// previewMaxLines and previewMaxRunes bound how much of a conflicting
// file's existing content Conflict.Preview shows: enough for a human to
// recognize what they would lose, never so much that a large hand-edited
// file floods the terminal or a JSON error's "details" field.
const (
	previewMaxLines = 20
	previewMaxRunes = 2000
)

// Result reports where Write wrote AGENTS.md and the architecture map.
type Result struct {
	AgentsPath       string
	ArchitecturePath string
}

// Conflict describes one Nise-owned generated file that Write found
// on disk but which no longer verifies as nise's own last-generated
// content — most likely a hand edit, possibly a foreign file that never
// came from nise at all.
type Conflict struct {
	// Path is the file's path, exactly as passed to Write's caller
	// (typically absolute, since Write joins it from root).
	Path string
	// Preview is a truncated view of the file's current on-disk content,
	// for a human deciding whether to lose it. See previewMaxLines and
	// previewMaxRunes for the truncation bounds.
	Preview string
}

// ConflictError reports that Write would have overwritten one or more
// files matched by Conflict, and refused because Force was not set. The
// caller (internal/cli/agents.go) is expected to show every Conflict's
// Preview and tell the user to retry with --force.
type ConflictError struct {
	Conflicts []Conflict
}

// Error implements error.
func (e *ConflictError) Error() string {
	paths := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		paths[i] = c.Path
	}
	return fmt.Sprintf(
		"%d generated file(s) would be overwritten but do not match nise's last generated content: %s",
		len(e.Conflicts), strings.Join(paths, ", "),
	)
}

// Write generates AGENTS.md and .nise/architecture.json for r and writes
// them under root (a generated project's root directory, the one
// containing nise.json). Unless force is true, it first verifies any
// existing file at either target path against the content hash embedded
// in it at generation time (hash.go); a file that does not verify is
// reported via a *ConflictError instead of being overwritten, and neither
// file is written — a partial write on a refusal would leave one output
// stale relative to the other. Passing force skips verification entirely
// and always overwrites both.
func Write(root string, r recipe.Recipe, force bool) (*Result, error) {
	out, err := Generate(r)
	if err != nil {
		return nil, err
	}

	agentsPath := filepath.Join(root, AgentsFileName)
	archPath := filepath.Join(root, ArchitectureDir, ArchitectureFileName)

	if !force {
		var conflicts []Conflict
		for _, cand := range []struct {
			path   string
			verify func([]byte) bool
		}{
			{agentsPath, verifyMarkdown},
			{archPath, verifyArchitecture},
		} {
			c, err := checkConflict(cand.path, cand.verify)
			if err != nil {
				return nil, err
			}
			if c != nil {
				conflicts = append(conflicts, *c)
			}
		}
		if len(conflicts) > 0 {
			return nil, &ConflictError{Conflicts: conflicts}
		}
	}

	if err := os.MkdirAll(filepath.Join(root, ArchitectureDir), 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Join(root, ArchitectureDir), err)
	}
	// #nosec G306 -- 0o644 matches internal/recipe.Save's own choice for
	// this project's other Nise-owned, non-secret generated file
	// (internal/recipe/json.go); neither AGENTS.md nor the architecture
	// map ever carries a secret (constraints.md §8), so there is no
	// confidentiality reason to restrict them to 0o600.
	if err := os.WriteFile(agentsPath, out.AgentsMD, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", agentsPath, err)
	}
	// #nosec G306 -- see the identical justification immediately above.
	if err := os.WriteFile(archPath, out.Architecture, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", archPath, err)
	}

	return &Result{AgentsPath: agentsPath, ArchitecturePath: archPath}, nil
}

// checkConflict reads path and reports a *Conflict when it exists but
// verify rejects its content, nil when path does not exist yet (nothing
// to lose) or verify accepts it (safe to overwrite), and a non-nil error
// only for a read failure other than the file's absence.
func checkConflict(path string, verify func([]byte) bool) (*Conflict, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is joined from Write's own root/AgentsFileName/ArchitectureDir constants, never from unvalidated external input.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if verify(data) {
		return nil, nil
	}
	return &Conflict{Path: path, Preview: preview(data)}, nil
}

// preview truncates data to at most previewMaxLines lines and
// previewMaxRunes runes (rune-based, not byte-based, so truncation never
// splits a multi-byte character), appending a marker when either bound
// was hit.
func preview(data []byte) string {
	lines := strings.Split(string(data), "\n")
	truncated := false
	if len(lines) > previewMaxLines {
		lines = lines[:previewMaxLines]
		truncated = true
	}

	runes := []rune(strings.Join(lines, "\n"))
	if len(runes) > previewMaxRunes {
		runes = runes[:previewMaxRunes]
		truncated = true
	}

	out := string(runes)
	if truncated {
		out += "\n... (truncated)"
	}
	return out
}
