package release_test

// A branch-protection setting that lives only in a web form is a claim nobody
// can review, and one nobody notices being turned off. The rulesets are
// committed under .github/rulesets/ as GitHub's own export format; these tests
// read them and fail when they stop requiring what docs/repository-security.md
// says the repository requires.
//
// What they cannot do is prove a ruleset is *active* on GitHub — nothing in a
// repository can, which is why the documentation says how to check that with
// `gh api`. What they do catch is the drift that actually happens: a job is
// added to CI, nobody adds it to the ruleset, and it silently stops gating
// anything.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type ruleset struct {
	Name         string `json:"name"`
	Target       string `json:"target"`
	Enforcement  string `json:"enforcement"`
	BypassActors []any  `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			Strict   bool `json:"strict_required_status_checks_policy"`
			Required []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	} `json:"rules"`
}

func readRuleset(t *testing.T, name string) ruleset {
	t.Helper()
	var parsed ruleset
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(".github", "rulesets", name))), &parsed); err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return parsed
}

func (r ruleset) has(rule string) bool {
	for _, declared := range r.Rules {
		if declared.Type == rule {
			return true
		}
	}
	return false
}

// The default branch is what every release is cut from.
func TestTheDefaultBranchIsProtected(t *testing.T) {
	t.Parallel()
	rules := readRuleset(t, "master.json")

	if rules.Target != "branch" || rules.Enforcement != "active" {
		t.Errorf("the master ruleset targets %q with enforcement %q; it must actively protect a branch", rules.Target, rules.Enforcement)
	}
	// A bypass actor is a hole in every rule below it, and this repository has
	// one maintainer who has no reason to need one.
	if len(rules.BypassActors) != 0 {
		t.Errorf("the master ruleset allows %d bypass actor(s); every rule below is optional for them", len(rules.BypassActors))
	}
	for _, rule := range []string{"deletion", "non_fast_forward", "required_linear_history", "required_signatures", "required_status_checks"} {
		if !rules.has(rule) {
			t.Errorf("the default branch does not require %q", rule)
		}
	}
}

// A moved tag is the classic supply-chain attack against a Go project:
// `go install …@v0.1.0` resolves a tag, and a tag that moved after publication
// serves different code under a version people have already audited.
func TestReleaseTagsCannotBeMovedOrDeleted(t *testing.T) {
	t.Parallel()
	rules := readRuleset(t, "tags.json")

	if rules.Target != "tag" || rules.Enforcement != "active" {
		t.Errorf("the tag ruleset targets %q with enforcement %q; it must actively protect tags", rules.Target, rules.Enforcement)
	}
	if len(rules.BypassActors) != 0 {
		t.Errorf("the tag ruleset allows %d bypass actor(s)", len(rules.BypassActors))
	}
	for _, rule := range []string{"deletion", "update", "non_fast_forward", "required_signatures"} {
		if !rules.has(rule) {
			t.Errorf("release tags do not require %q", rule)
		}
	}
	// It has to cover the tags the release workflow actually triggers on.
	var covers bool
	for _, pattern := range rules.Conditions.RefName.Include {
		if pattern == "refs/tags/v*" {
			covers = true
		}
	}
	if !covers {
		t.Errorf("the tag ruleset covers %v, and .github/workflows/release.yml triggers on refs/tags/v*",
			rules.Conditions.RefName.Include)
	}
}

// The drift that actually happens: a job is added to CI, nobody adds it to the
// ruleset, and it silently stops gating anything.
func TestEveryCIJobIsARequiredCheck(t *testing.T) {
	t.Parallel()

	required := map[string]bool{}
	for _, rule := range readRuleset(t, "master.json").Rules {
		for _, check := range rule.Parameters.Required {
			required[check.Context] = true
		}
		if rule.Type == "required_status_checks" && !rule.Parameters.Strict {
			t.Error("required status checks are not strict, so a branch that passed CI before an incompatible change landed can still merge")
		}
	}
	if len(required) == 0 {
		t.Fatal("the master ruleset requires no status checks, so this test proves nothing")
	}

	// A matrixed job's check context is its display name plus the matrix
	// value, which is why the runners are read rather than assumed.
	workflow := readFile(t, ".github/workflows/ci.yml")
	jobName := regexp.MustCompile(`(?m)^  ([a-z-]+):\n    name: (.+)$`)
	runners := regexp.MustCompile(`os: \[([^\]]+)\]`).FindStringSubmatch(workflow)

	var expected []string
	for _, match := range jobName.FindAllStringSubmatch(workflow, -1) {
		display := strings.TrimSpace(match[2])
		if match[1] == "test" && runners != nil {
			for _, runner := range strings.Split(runners[1], ",") {
				expected = append(expected, display+" ("+strings.TrimSpace(runner)+")")
			}
			continue
		}
		expected = append(expected, display)
	}
	if len(expected) == 0 {
		t.Fatal("no named jobs were found in ci.yml, so this test proves nothing")
	}

	for _, context := range expected {
		if !required[context] {
			t.Errorf("ci.yml runs %q and the master ruleset does not require it, so it gates nothing", context)
		}
		delete(required, context)
	}
	if len(required) > 0 {
		var stale []string
		for context := range required {
			stale = append(stale, context)
		}
		sort.Strings(stale)
		t.Errorf("the master ruleset requires %v, which ci.yml no longer runs; a required check that never reports blocks every merge", stale)
	}
}

// The rulesets are committed to be read. A page describing protections that
// names none of the files holding them sends a reader to a web form.
func TestTheProtectionsAreDocumented(t *testing.T) {
	t.Parallel()
	doc := readFile(t, "docs/repository-security.md")

	for _, mention := range []string{
		".github/rulesets/",
		"gh api repos/drilonrecica/nise-and-go/rulesets",
		// The honest part, which is the reason this page exists rather than a
		// paragraph in checks.md.
		"an attacker holding the maintainer's GitHub account can do everything",
		"sum.golang.org",
	} {
		if !strings.Contains(doc, mention) {
			t.Errorf("docs/repository-security.md does not mention %q", mention)
		}
	}
	if !strings.Contains(readFile(t, ".github/rulesets/README.md"), "Import a ruleset") {
		t.Error("the rulesets directory does not say how to apply them, so committing them is documentation of an intention nobody can act on")
	}
}

// Every committed ruleset is valid JSON GitHub would accept, and is covered by
// a test above. A third file added later that nothing reads is worse than no
// file: it looks like protection.
func TestEveryCommittedRulesetIsChecked(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".github", "rulesets"))
	if err != nil {
		t.Fatalf("reading the rulesets directory: %v", err)
	}
	checked := map[string]bool{"master.json": true, "tags.json": true}
	var found int
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found++
		if !checked[entry.Name()] {
			t.Errorf("%s is committed as a ruleset and no test reads it", entry.Name())
		}
		var parsed ruleset
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(".github", "rulesets", entry.Name()))), &parsed); err != nil {
			t.Errorf("%s is not valid JSON: %v", entry.Name(), err)
		}
		if parsed.Name == "" {
			t.Errorf("%s declares no name, so GitHub would not import it", entry.Name())
		}
	}
	if found != len(checked) {
		t.Errorf("%d rulesets are committed and %d are checked", found, len(checked))
	}
}
