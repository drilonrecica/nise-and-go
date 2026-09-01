package freeze_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// The frozen surface, written out here.
//
// It is a third place, deliberately: the code declares what exists, the
// documentation says why, and this says what was *intended*. Two sources
// derived from each other agree by construction, which is not the same as
// being right.
var (
	frozenProfiles = []string{"go-chi-postgres-svelte"}
	frozenModules  = []string{"notifications", "organizations", "totp", "uploads"}
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), name)) // #nosec G304 -- a fixed path in this repository.
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

// One profile. A second one is a second golden path to keep coherent, test,
// and document, and the blueprint says a later profile is separately tested
// rather than added beside this one.
func TestTheProfileSetIsFrozen(t *testing.T) {
	t.Parallel()

	var got []string
	for _, profile := range recipe.Profiles() {
		got = append(got, string(profile))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(frozenProfiles, ",") {
		t.Errorf("the profiles are %v and V0.1 freezes %v.\n"+
			"A profile is a whole golden path: adding one means testing, documenting, and keeping it coherent.", got, frozenProfiles)
	}
}

// Four modules, named in DECISIONS.md. "No others in V0.1" is the whole
// sentence, and a fifth would arrive as a one-line change to a slice.
func TestTheModuleSetIsFrozen(t *testing.T) {
	t.Parallel()

	var got []string
	for _, module := range recipe.Modules() {
		got = append(got, string(module))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(frozenModules, ",") {
		t.Errorf("the modules are %v and V0.1 freezes %v.\n"+
			"An addition must replace an existing item or prove an application cannot ship safely without it.", got, frozenModules)
	}
}

// Every dependency a generated project pins is justified in
// docs/dependencies.md, by name.
//
// This is the check that actually holds the line, because a dependency is the
// easiest thing to add: one line in a version list, and it is in every project
// anybody generates from then on. Requiring the name to appear in the document
// means adding one is a change somebody has to write a paragraph for — which
// is the whole mechanism, since the paragraph is where the justification
// either exists or visibly does not.
//
// It checks presence rather than parsing the table. The document's shape is
// prose and should stay free to change; what must not change silently is that
// a pinned dependency is discussed somewhere in it.
func TestEveryPinnedDependencyIsJustified(t *testing.T) {
	t.Parallel()

	allowlist := readFile(t, "docs/dependencies.md")

	pinned := generator.GoDependencies()
	pinned = append(pinned, generator.FrontendRuntimeDependencies()...)
	pinned = append(pinned, generator.FrontendDependencies()...)
	if len(pinned) == 0 {
		t.Fatal("no pinned dependencies were found, so this test proves nothing")
	}

	for _, dependency := range pinned {
		// The framework's own module is the project, not a dependency of it.
		if dependency.Name == generator.NiseModulePath {
			continue
		}
		if !strings.Contains(allowlist, dependency.Name) {
			t.Errorf("a generated project pins %s and docs/dependencies.md does not mention it.\n"+
				"A dependency enters every project anybody generates. If this one belongs, say why there.",
				dependency.Name)
		}
	}
}

// And the count is pinned too, so a dependency cannot be added by mentioning
// it in the document at the same time.
//
// The number is deliberately dumb. Its whole job is to make the diff of an
// addition contain a line that says "this is one more than before", in a file
// whose only purpose is to notice.
func TestTheNumberOfPinnedDependenciesIsFrozen(t *testing.T) {
	t.Parallel()

	const (
		frozenGoDependencies      = 11
		frozenFrontendRuntimeDeps = 2
		frozenFrontendDevDeps     = 20
	)

	for _, counted := range []struct {
		what   string
		got    int
		frozen int
	}{
		{"Go module requirements", len(generator.GoDependencies()), frozenGoDependencies},
		{"frontend runtime packages", len(generator.FrontendRuntimeDependencies()), frozenFrontendRuntimeDeps},
		{"frontend build packages", len(generator.FrontendDependencies()), frozenFrontendDevDeps},
	} {
		if counted.got != counted.frozen {
			t.Errorf("a generated project pins %d %s and V0.1 freezes %d.\n"+
				"If this is deliberate, change the number here in the same commit and say why in docs/dependencies.md.",
				counted.got, counted.what, counted.frozen)
		}
	}
}

// The freeze is not in force until the V0.1 checklist passes, and the status
// page is where that is recorded. This keeps the two from drifting into a
// state where the tests above enforce a freeze nobody declared, or the page
// declares one nothing enforces.
func TestTheStatusPageStillReportsWhatIsUnfinished(t *testing.T) {
	t.Parallel()

	status := readFile(t, "docs/project-status.md")
	if !strings.Contains(status, "## What is not finished") {
		t.Error("docs/project-status.md no longer reports what is unfinished; the V0.1 gaps are what decide whether the freeze can begin")
	}
	if !strings.Contains(status, "## What is proved, and how") {
		t.Error("docs/project-status.md no longer names the evidence for each acceptance criterion")
	}
}
