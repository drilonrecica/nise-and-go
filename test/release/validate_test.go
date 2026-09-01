package release_test

// A release is validated after it is published, by
// .github/workflows/release-validate.yml driving cmd/release-validate. These
// tests pin the properties that make that validation mean something: it reads
// the published artifacts rather than a rebuild, it verifies provenance before
// trusting anything, it checks the release somebody would roll back to as well
// as the one that just went out, and the validator's idea of what a release
// contains matches the configuration that produces one.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func validateWorkflow(t *testing.T) string {
	t.Helper()
	return readFile(t, ".github/workflows/release-validate.yml")
}

func validator(t *testing.T) string {
	t.Helper()
	return readFile(t, "cmd/release-validate/main.go")
}

// A tag push produces a draft, whose assets are not public and whose asset
// set a human is still about to change. Validating then would validate
// something nobody can download.
func TestValidationRunsOnPublishAndNotOnTheTagPush(t *testing.T) {
	t.Parallel()
	workflow := validateWorkflow(t)

	if !regexp.MustCompile(`(?m)^\s*release:\s*$`).MatchString(workflow) ||
		!strings.Contains(workflow, "types: [published]") {
		t.Error("release-validate.yml does not run on the release publish event")
	}
	if regexp.MustCompile(`(?m)^\s*tags:\s*$`).MatchString(workflow) {
		t.Error("release-validate.yml runs on a tag push, which validates a draft nobody can download")
	}
	// Re-validating an old release on demand is the rollback rehearsal, and
	// the retry path when a run fails for an unrelated reason.
	if !strings.Contains(workflow, "workflow_dispatch:") {
		t.Error("release-validate.yml cannot be run against an already-published release")
	}
}

// The rollback validation. "Roll back to the previous version" is a promise
// the release notes make and nothing keeps — the previous release's assets
// decay in silence until somebody needs them.
func TestTheRollbackTargetIsValidatedToo(t *testing.T) {
	t.Parallel()
	workflow := validateWorkflow(t)

	if !strings.Contains(workflow, "gh release list") {
		t.Fatal("release-validate.yml resolves no earlier release, so nothing checks the version somebody would roll back to")
	}
	// A draft's assets are not public, and rolling back onto a release
	// candidate is not a rollback anybody wants.
	for _, exclusion := range []string{"--exclude-drafts", "--exclude-pre-releases"} {
		if !strings.Contains(workflow, exclusion) {
			t.Errorf("the rollback target is resolved without %s, so it may name a release nobody can install", exclusion)
		}
	}
	if !strings.Contains(workflow, "matrix:") || !strings.Contains(workflow, "matrix.tag") {
		t.Error("release-validate.yml does not validate its two tags as a matrix, so one result hides the other")
	}
	// Both results are reported. A rollback target that broke is worth
	// knowing about even when the release that just went out is perfect.
	if !strings.Contains(workflow, "fail-fast: false") {
		t.Error("the validation matrix is fail-fast, so a failure on one tag cancels the other's result")
	}
}

// Verify, then trust. Pinned by position rather than by presence: both steps
// existing in the wrong order is the bug this prevents, and a test for
// presence alone passes on it.
func TestProvenanceIsVerifiedBeforeAnythingIsTrusted(t *testing.T) {
	t.Parallel()
	workflow := validateWorkflow(t)

	verify := strings.Index(workflow, "gh attestation verify")
	if verify < 0 {
		t.Fatal("release-validate.yml verifies no build attestation, so it validates whatever the release currently holds")
	}
	for _, later := range []string{"go run ./cmd/release-validate", "nise --json version"} {
		at := strings.Index(workflow, later)
		if at < 0 {
			t.Errorf("release-validate.yml never runs %q", later)
			continue
		}
		if at < verify {
			t.Errorf("%q runs before the attestation is verified", later)
		}
	}
}

// The validator asks the binary what it is, and compares that against the tag
// and the tag's commit. Nothing else in the release can catch a binary built
// from the wrong commit or with no ldflags applied.
func TestTheReleasedBinaryIsMadeToIdentifyItself(t *testing.T) {
	t.Parallel()
	workflow := validateWorkflow(t)

	if !strings.Contains(workflow, "-version-report") {
		t.Error("the validation never compares the binary's own version report against the release")
	}
	if !strings.Contains(workflow, `git rev-parse "${TAG}^{commit}"`) || !strings.Contains(workflow, "-commit") {
		t.Error("the validation never compares the binary's commit against the tag's commit")
	}
	if !strings.Contains(workflow, "fetch-depth: 0") {
		t.Error("the checkout is shallow, so the tag's commit cannot be resolved and the commit check silently compares nothing")
	}
}

// The validator writes out the six platforms rather than reading them from
// .goreleaser.yaml, so that a platform added to the release has to be
// consciously added or excluded here. This is the third place that makes
// those two agree.
func TestTheValidatorKnowsTheReleaseTargets(t *testing.T) {
	t.Parallel()
	source := validator(t)

	for _, target := range releaseTargets {
		parts := strings.SplitN(target, "/", 2)
		pair := `{"` + parts[0] + `", "` + parts[1] + `"}`
		if !strings.Contains(source, pair) {
			t.Errorf("cmd/release-validate does not validate %s, which this project releases", target)
		}
	}
	// And nothing beyond them: a validator expecting a seventh archive fails
	// every real release.
	declared := regexp.MustCompile(`\{"(\w+)", "(\w+)"\}`).FindAllStringSubmatch(source, -1)
	if len(declared) != len(releaseTargets) {
		t.Errorf("cmd/release-validate names %d platform pairs, and this project releases %d", len(declared), len(releaseTargets))
	}
}

// The validator opens each archive and compares its entries against what
// .goreleaser.yaml puts in one. Two lists that drift apart make the check
// pass on an archive nobody would want.
func TestTheValidatorExpectsWhatTheArchivesActuallyContain(t *testing.T) {
	t.Parallel()
	source := validator(t)

	packed := listValues(t, goreleaserConfig(t), "files")
	if len(packed) == 0 {
		t.Fatal(".goreleaser.yaml packs no named files into its archives; this test found no list to check")
	}
	for _, name := range packed {
		if !strings.Contains(source, `"`+name+`"`) {
			t.Errorf("the archives contain %s and cmd/release-validate does not expect it", name)
		}
	}
	// The name the release actually writes. GoReleaser's {{ .Version }}
	// strips the tag's "v", which is the drift this pins.
	if !strings.Contains(source, `"nise_%s_%s_%s.tar.gz"`) || !strings.Contains(source, `"nise_%s_%s_%s.zip"`) {
		t.Error("cmd/release-validate does not build the archive names .goreleaser.yaml's name_template produces")
	}
	if !strings.Contains(goreleaserConfig(t), `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`) {
		t.Error("the archive name template changed; cmd/release-validate builds the old names and would report every archive missing")
	}
}

// Event and input data reaches a shell only through a quoted env variable.
// GitHub expands ${{ }} into the script text before any shell sees it, so a
// release title containing a shell metacharacter would otherwise be executed
// rather than compared.
func TestWorkflowsNeverInterpolateEventDataIntoAShell(t *testing.T) {
	t.Parallel()

	workflows, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("listing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows were found, so this test proves nothing")
	}

	untrusted := regexp.MustCompile(`\$\{\{\s*(github\.event\b|inputs\b|needs\b)[^}]*\}\}`)
	scanned := 0
	for _, path := range workflows {
		data, err := os.ReadFile(path) // #nosec G304 -- path came from Glob over this repository.
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		lines := runScriptLines(string(data))
		scanned += len(lines)
		for _, line := range lines {
			if untrusted.MatchString(line) {
				t.Errorf("%s expands event data directly into a shell script: %s",
					filepath.Base(path), strings.TrimSpace(line))
			}
		}
	}
	// A scanner that finds no shell scripts makes this test vacuous, and the
	// workflows in this repository are almost entirely shell.
	if scanned == 0 {
		t.Fatal("no run: script lines were found in any workflow, so this test proves nothing")
	}
}

// runScriptLines returns the lines that are part of a `run: |` block. It is a
// line scanner rather than a YAML parser, for the reason listValues gives:
// these are this repository's own files and their shape is stable.
func runScriptLines(workflow string) []string {
	var script []string
	lines := strings.Split(workflow, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		// A single-line `run:` is its own script.
		if strings.HasPrefix(trimmed, "run:") && trimmed != "run: |" {
			script = append(script, lines[i])
			continue
		}
		if trimmed != "run: |" {
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		for _, body := range lines[i+1:] {
			if strings.TrimSpace(body) == "" {
				script = append(script, body)
				continue
			}
			if len(body)-len(strings.TrimLeft(body, " ")) <= indent {
				break
			}
			script = append(script, body)
		}
	}
	return script
}
