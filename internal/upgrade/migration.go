package upgrade

import "strings"

// Migration describes what moving from one runtime version to the next
// requires beyond regenerating Nise-owned files.
//
// Migrations are declared, not discovered. A version pair with no entry here
// needs nothing but regeneration and a go.mod bump, and saying so explicitly
// is the point: "no migration exists" and "no migration was found" must not
// look the same to a reader.
type Migration struct {
	// From and To are runtime versions, "v"-prefixed, exactly as they appear
	// in a project recipe's runtimeVersion.
	From, To string
	// Summary is one line naming what changed, for the upgrade report.
	Summary string
	// Codemods are the exact textual rewrites that can be applied to
	// application-owned Go source without judgement. Keep this list small: a
	// rewrite that is only *usually* right belongs in Notes.
	Codemods []Codemod
	// Removed are Nise-owned files this version stops writing, relative to the
	// project root and slash-separated.
	//
	// They are declared rather than discovered, and they have to be: an
	// upgrade can only plan a project with the templates it *has*, so the file
	// set an older nise wrote is not something a newer one can compute. The
	// alternative — recording every written path in the project and diffing
	// against it — buys a manifest that has to stay correct through every hand
	// edit, to detect a case that happens once per removed subsystem and is
	// known to whoever removed it.
	//
	// An orphan matters: a .gen.go from a subsystem that no longer exists
	// still compiles, and still refers to things that may not.
	Removed []string
	// Notes are the changes a person has to make. They are printed, never
	// applied. A note is not a failure of the tooling — it is the tooling
	// declining to guess at something it cannot verify.
	Notes []string
}

// Codemod is one exact textual replacement.
//
// Exact and textual is the entire safety argument. An AST rewrite is more
// powerful and can be wrong in ways a developer will not notice in a diff; a
// literal replacement of a string that does not occur is a no-op, and one that
// does occur is visible in `git diff` as precisely the substitution that was
// announced. Every application it makes is counted and reported, so a codemod
// that fires somewhere unexpected is in the output rather than only in the
// working tree.
type Codemod struct {
	// Describe is what this rewrite is for, in one line.
	Describe string
	// Old is the exact text to replace. It must be specific enough that a
	// coincidental match is not plausible — an import path, a qualified
	// identifier, never a bare word.
	Old string
	// New is what replaces it.
	New string
}

// migrations is the ordered list of declared migrations.
//
// It is empty, and that is the honest state of this project rather than an
// omission: v0.1.0 is the first release, so there is no earlier version to
// migrate from. The machinery is built and tested against fixture migrations
// (see migration_test.go and upgrade_test.go) so that the first real one is a
// table entry rather than a new subsystem written under release pressure.
//
// Adding one: append it here, with From and To naming consecutive runtime
// versions. Path assembles a chain across several versions automatically.
var migrations []Migration

// Path returns the migrations that carry a project from one runtime version
// to another, in order.
//
// Missing links are reported rather than skipped. An upgrade that silently
// jumped from v0.1.0 to v0.4.0 because no v0.2.0 entry existed would leave a
// project half-migrated with nothing in the output to say so.
func path(from, to string, declared []Migration) (chain []Migration, missing []string) {
	if from == to {
		return nil, nil
	}
	byFrom := make(map[string]Migration, len(declared))
	for _, m := range declared {
		byFrom[m.From] = m
	}

	at := from
	// The bound is the number of declared migrations: a chain cannot be
	// longer than the table it is assembled from, and a cycle in a
	// hand-written table should stop rather than hang.
	for range len(declared) + 1 {
		if at == to {
			return chain, nil
		}
		next, ok := byFrom[at]
		if !ok {
			return chain, []string{at}
		}
		chain = append(chain, next)
		at = next.To
	}
	if at != to {
		return chain, []string{at}
	}
	return chain, nil
}

// apply runs one codemod over source, returning the result and how many
// occurrences it replaced.
func (c Codemod) apply(source string) (string, int) {
	count := strings.Count(source, c.Old)
	if count == 0 {
		return source, 0
	}
	return strings.ReplaceAll(source, c.Old, c.New), count
}

// Declared returns the migrations this nise ships, so a test can check the
// table is well formed without the table being writable from outside.
func Declared() []Migration {
	out := make([]Migration, len(migrations))
	copy(out, migrations)
	return out
}
