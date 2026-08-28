package check

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/agentsfile"
	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

const (
	invariantHeaders = "Every generated file still declares the ownership model it was written with (ADR 0003)."
	invariantContent = "The Nise-owned files that carry their own content hash still verify against it."
)

// headerWindowLines is how far into a file the ownership header is looked
// for. Five lines is enough for every shape `nise new` writes — line 1 for a
// Go file or a Markdown comment, line 2 for the HTML placeholder whose
// doctype must come first — and short enough that a mention of the header
// text in a file's prose is not mistaken for the header itself. The
// generated README.md, for one, quotes the marker verbatim at line 143 while
// documenting the ownership rules; a whole-file substring scan would read
// that as a clobbered app-owned file.
const headerWindowLines = 5

// checkOwnershipHeaders verifies that every generated file still present on
// disk declares the ownership it was written with.
//
// The two directions are deliberately asymmetric, and the asymmetry is the
// whole judgment in this check:
//
//   - A Nise-owned file MUST still carry the Nise-owned header. Losing it is
//     unambiguous evidence of a hand edit, and — unlike the regeneration
//     check — it stays conclusive no matter which nise built the file, because
//     the header records no version (see internal/generator's manifest.go).
//
//   - An app-owned file must merely NOT carry the Nise-owned header. Its
//     absence proves nothing: the application owns the whole file and is free
//     to delete the courtesy header, reorganize the file, or rewrite it from
//     scratch. Requiring the app-owned header to survive would be enforcing
//     starter conformity on a file whose entire point is that the application
//     may do as it likes with it. What the check does catch is the one thing
//     that is a real violation: generated output having landed on top of a
//     file the application owns.
//
// A generated file that is simply gone is not a violation either. Deleting
// an app-owned file is the application's prerogative, and a deleted
// Nise-owned file is caught, with a far more useful message, by the
// regeneration check.
func checkOwnershipHeaders(root string, plan []generator.File) Check {
	if plan == nil {
		return skip(CheckOwnershipHeaders, invariantHeaders,
			"no valid recipe, so there is no way to know which files nise owns in this project; see the \""+CheckRecipe+"\" check")
	}

	var violations []string
	var details []string
	present, absent := 0, 0

	for _, f := range plan {
		// nise.json is Nise-owned and carries no header: JSON has no
		// comment syntax to put one in. Its integrity is covered twice
		// over instead — the recipe check parses it, and the
		// regeneration check compares it byte for byte.
		if f.Path == recipe.FileName {
			continue
		}

		head, err := readHead(filepath.Join(root, filepath.FromSlash(f.Path)), headerWindowLines)
		if errors.Is(err, fs.ErrNotExist) {
			absent++
			continue
		}
		if err != nil {
			violations = append(violations, f.Path)
			details = append(details, fmt.Sprintf("%s: could not be read (%s)", f.Path, err))
			continue
		}
		present++

		marked := strings.Contains(head, generator.NiseOwnedHeader)
		switch {
		case f.Owner == generator.OwnerNise && !marked:
			violations = append(violations, f.Path)
			details = append(details, fmt.Sprintf(
				"%s is nise-owned but its first %d lines no longer carry %q", f.Path, headerWindowLines, generator.NiseOwnedHeader))
		case f.Owner == generator.OwnerApp && marked:
			violations = append(violations, f.Path)
			details = append(details, fmt.Sprintf(
				"%s is application-owned but now carries the nise-owned header %q", f.Path, generator.NiseOwnedHeader))
		}
	}

	if len(violations) == 0 {
		return pass(CheckOwnershipHeaders, invariantHeaders, fmt.Sprintf(
			"%d generated file(s) still declare the ownership they were written with; %d have since been removed from the project, which is allowed",
			present, absent,
		))
	}

	slices.Sort(violations)
	c := fail(CheckOwnershipHeaders, invariantHeaders,
		strings.Join(details, "; "),
		"Restore the listed files from version control. A nise-owned file that lost its header was hand-edited and its edits will be lost on the next generation; an application-owned file that gained one was overwritten by generated output, which nise must never do — please report that as a bug.",
	)
	c.Paths = violations
	return c
}

// checkOwnershipContent verifies the two Nise-owned files that carry their
// own content hash — AGENTS.md and .nise/architecture.json — against it.
//
// This is the one hand-edit check that is conclusive regardless of which
// nise generated the project, because the hash covers the file's own body
// rather than comparing it against what today's templates would produce.
//
// It reaches that verification through internal/agentsfile rather than
// recomputing the hashes here. internal/agentsfile owns the hash scheme
// (see its hash.go: the hash is embedded in the file, not in a sidecar,
// precisely so each file is self-verifying), and its verification is
// reachable only through Write, which refuses with a *ConflictError when a
// file does not verify. So the two files are copied into a temporary
// directory and Write is aimed at the copy: the project is never written
// to, and the verification rule stays defined in exactly one place instead
// of being duplicated into a second, silently divergent implementation
// here.
func checkOwnershipContent(root string, r *recipe.Recipe) Check {
	if r == nil {
		return skip(CheckOwnershipContent, invariantContent,
			"no valid recipe, so the expected content cannot be derived; see the \""+CheckRecipe+"\" check")
	}

	paths := []string{agentsfile.AgentsFileName, filepath.ToSlash(filepath.Join(agentsfile.ArchitectureDir, agentsfile.ArchitectureFileName))}

	scratch, err := os.MkdirTemp("", "nise-check-ownership-")
	if err != nil {
		return skip(CheckOwnershipContent, invariantContent,
			fmt.Sprintf("could not create a temporary directory to verify against (%s)", err))
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	var missing []string
	for _, rel := range paths {
		src := filepath.Join(root, filepath.FromSlash(rel))
		data, readErr := os.ReadFile(src) // #nosec G304 -- rel comes from internal/agentsfile's own filename constants, never from external input.
		if errors.Is(readErr, fs.ErrNotExist) {
			missing = append(missing, rel)
			continue
		}
		if readErr != nil {
			return skip(CheckOwnershipContent, invariantContent,
				fmt.Sprintf("could not read %s (%s)", rel, readErr))
		}
		dst := filepath.Join(scratch, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o750); mkErr != nil {
			return skip(CheckOwnershipContent, invariantContent,
				fmt.Sprintf("could not stage %s for verification (%s)", rel, mkErr))
		}
		// #nosec G703 -- dst is a path under a directory os.MkdirTemp
		// created moments ago for this call alone, joined with one of
		// internal/agentsfile's own two filename constants. Neither half
		// comes from unvalidated input, and the project being checked is
		// never the write target.
		if wErr := os.WriteFile(dst, data, 0o600); wErr != nil {
			return skip(CheckOwnershipContent, invariantContent,
				fmt.Sprintf("could not stage %s for verification (%s)", rel, wErr))
		}
	}

	if len(missing) == len(paths) {
		c := skip(CheckOwnershipContent, invariantContent,
			"neither self-verifying file is present in this project; nothing to verify")
		c.Paths = missing
		return c
	}

	_, writeErr := agentsfile.Write(scratch, *r, false)
	var conflict *agentsfile.ConflictError
	if errors.As(writeErr, &conflict) {
		offending := make([]string, 0, len(conflict.Conflicts))
		for _, c := range conflict.Conflicts {
			rel, relErr := filepath.Rel(scratch, c.Path)
			if relErr != nil {
				rel = c.Path
			}
			offending = append(offending, filepath.ToSlash(rel))
		}
		slices.Sort(offending)
		out := fail(CheckOwnershipContent, invariantContent,
			fmt.Sprintf("%s no longer match the content hash embedded in them at generation time", strings.Join(offending, " and ")),
			`Restore the listed files from version control, or accept the loss and regenerate them with "nise agents --force".`,
		)
		out.Paths = offending
		return out
	}
	if writeErr != nil {
		return skip(CheckOwnershipContent, invariantContent,
			fmt.Sprintf("could not verify the self-verifying files (%s)", writeErr))
	}

	verified := make([]string, 0, len(paths))
	for _, p := range paths {
		if !slices.Contains(missing, p) {
			verified = append(verified, p)
		}
	}
	c := pass(CheckOwnershipContent, invariantContent, fmt.Sprintf(
		"%s still match the content hash embedded in them at generation time", strings.Join(verified, " and ")))
	c.Paths = verified
	if len(missing) > 0 {
		c.Detail += fmt.Sprintf("; %s is not present in this project", strings.Join(missing, " and "))
	}
	return c
}

// readHead returns the first n lines of the file at path.
func readHead(path string, n int) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is the project root joined with a path from the generator's own template manifest, never with external input.
	if err != nil {
		return "", err
	}
	lines := strings.SplitN(string(data), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n"), nil
}
