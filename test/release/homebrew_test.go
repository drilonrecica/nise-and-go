package release_test

// The Homebrew tap is updated by .github/workflows/homebrew.yml, which runs
// when a human publishes a drafted release. These tests pin the properties
// that make that automation safe rather than merely convenient: the tap
// updates only after publish, only from attested canonical artifacts, and
// only through the generator this repository owns and tests.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func homebrewWorkflow(t *testing.T) string {
	t.Helper()
	return readFile(t, ".github/workflows/homebrew.yml")
}

// The release workflow drafts; nothing is installable until a human presses
// publish. A formula generated at tag time would point at URLs that do not
// exist yet — so the tap updates on the publish event, and the GoReleaser
// configuration must not grow a brews/casks section that publishes during
// the tag build.
func TestTheTapUpdatesOnlyWhenAHumanPublishes(t *testing.T) {
	t.Parallel()
	workflow := homebrewWorkflow(t)

	if !regexp.MustCompile(`(?m)^\s*types:\s*\[published\]\s*$`).MatchString(workflow) {
		t.Error("the tap workflow does not trigger on release type [published]")
	}
	if strings.Contains(workflow, "tags:") {
		t.Error("the tap workflow triggers on a tag push, before a human has published anything")
	}

	config := goreleaserConfig(t)
	for _, key := range []string{"brews:", "homebrew_casks:"} {
		if regexp.MustCompile(`(?m)^` + key).MatchString(config) {
			t.Errorf(".goreleaser.yaml declares %q, which would push a formula while the release is still a draft", key)
		}
	}
}

// The tap is what `brew install` gives everybody, so a prerelease must never
// reach it: the event condition skips prereleases, and the dispatch path —
// which can name any tag — re-checks both draft and prerelease state.
func TestTheTapNeverServesADraftOrPrerelease(t *testing.T) {
	t.Parallel()
	workflow := homebrewWorkflow(t)

	if !strings.Contains(workflow, "github.event.release.prerelease == false") {
		t.Error("the tap workflow does not skip prerelease publish events")
	}
	for _, check := range []string{".isDraft", ".isPrerelease"} {
		if !strings.Contains(workflow, check) {
			t.Errorf("the tap workflow does not re-check %s for a manually named tag", check)
		}
	}
}

// The formula embeds hashes from the release's checksums.txt, and brew
// verifies every download against them — so the attestation on that one
// file transitively covers every archive the formula installs. Verifying it
// before generating is what makes the tap serve what the release workflow
// built, rather than whatever is attached to the release right now.
func TestTheTapUpdaterVerifiesTheAttestationBeforeGenerating(t *testing.T) {
	t.Parallel()
	workflow := homebrewWorkflow(t)

	verify := strings.Index(workflow, "gh attestation verify checksums.txt")
	generate := strings.Index(workflow, "go run ./cmd/homebrew-formula")
	if verify < 0 {
		t.Fatal("the tap workflow does not verify the attestation on checksums.txt")
	}
	if generate < 0 {
		t.Fatal("the tap workflow does not run the formula generator this repository owns and tests")
	}
	if generate < verify {
		t.Error("the formula is generated before the attestation is verified")
	}
}

// The formula comes from canonical artifacts — the published checksums.txt —
// not from a rebuild, which could silently differ from what people who
// verify checksums by hand downloaded.
func TestTheFormulaComesFromTheCanonicalChecksums(t *testing.T) {
	t.Parallel()
	workflow := homebrewWorkflow(t)

	if !strings.Contains(workflow, "gh release download") || !strings.Contains(workflow, "--pattern checksums.txt") {
		t.Error("the tap workflow does not download the published checksums.txt")
	}
	if !strings.Contains(workflow, "-checksums checksums.txt") {
		t.Error("the generator is not fed the downloaded checksums.txt")
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), "cmd", "homebrew-formula")); err != nil {
		t.Errorf("cmd/homebrew-formula does not exist: %v", err)
	}
}

// The golden formula is the reviewable record of what the tap serves. It
// must cover exactly the release platforms Homebrew runs on: the six pairs
// minus the two Windows ones.
func TestTheFormulaCoversTheReleasePairsMinusWindows(t *testing.T) {
	t.Parallel()
	golden := readFile(t, filepath.Join("cmd", "homebrew-formula", "testdata", "nise.rb"))

	var want []string
	for _, pair := range releaseTargets {
		if !strings.HasPrefix(pair, "windows/") {
			want = append(want, strings.ReplaceAll(pair, "/", "_"))
		}
	}
	for _, pair := range want {
		if !strings.Contains(golden, "_"+pair+".tar.gz") {
			t.Errorf("the formula does not install the %s archive", pair)
		}
	}
	if got := strings.Count(golden, `url "`); got != len(want) {
		t.Errorf("the formula names %d archives, and Homebrew covers %d release platforms", got, len(want))
	}
	if strings.Contains(golden, "windows") {
		t.Error("the formula mentions windows, which Homebrew does not run on")
	}
}
