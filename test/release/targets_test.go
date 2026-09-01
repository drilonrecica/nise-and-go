package release_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The six platform pairs this project releases for.
//
// They are written out here rather than derived from either file, so that a
// change to the configuration and a change to the documentation both have to
// pass through a third place that says what was intended. Two sources derived
// from each other always agree; that is not the same as being right.
var releaseTargets = []string{
	"darwin/amd64",
	"darwin/arm64",
	"linux/amd64",
	"linux/arm64",
	"windows/amd64",
	"windows/arm64",
}

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
	data, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

func goreleaserConfig(t *testing.T) string {
	t.Helper()
	return readFile(t, ".goreleaser.yaml")
}

// listValues reads the `- item` lines that follow a `key:` line.
//
// This is not a YAML parser and does not try to be. `goreleaser check`
// already proves the file is valid YAML, and the standard library has no YAML
// reader — pulling one in for six string comparisons would put a dependency
// in the allowlist to check a list this repository wrote itself. The
// configuration is ours and its shape is stable, so a line scan is honest
// here in a way it would not be for somebody else's file.
//
// It returns nil when the key is absent, which every caller treats as a
// failure rather than as an empty list.
func listValues(t *testing.T, source, key string) []string {
	t.Helper()

	lines := strings.Split(source, "\n")
	start := -1
	var indent int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == key+":" {
			start = i + 1
			indent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if start < 0 {
		return nil
	}

	var values []string
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A line indented no further than the key ends the list. Without
		// this a missing list would silently absorb the rest of the file.
		if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		values = append(values, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}
	return values
}

func TestTheReleaseBuildsExactlyTheSupportedPairs(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	goos := listValues(t, source, "goos")
	goarch := listValues(t, source, "goarch")
	if len(goos) == 0 || len(goarch) == 0 {
		t.Fatalf(".goreleaser.yaml declares goos=%v goarch=%v; this test found no platform list to check", goos, goarch)
	}

	var produced []string
	for _, o := range goos {
		for _, a := range goarch {
			produced = append(produced, o+"/"+a)
		}
	}
	sort.Strings(produced)

	if strings.Join(produced, " ") != strings.Join(releaseTargets, " ") {
		t.Errorf("the release builds %v, and this project releases for %v", produced, releaseTargets)
	}
}

// docs/supported-platforms.md is what somebody reads to find out whether their
// machine is supported. A table promising a platform the release does not
// build is worse than an honest omission.
func TestTheDocumentedPlatformsAreTheOnesBuilt(t *testing.T) {
	t.Parallel()
	doc := readFile(t, "docs/supported-platforms.md")

	// Read the table rows rather than searching for names, so a row that says
	// "no" is not satisfied by the operating system appearing in it.
	row := regexp.MustCompile(`(?m)^\|\s*(Linux|macOS|Windows)\s*\|\s*(yes|no)\s*\|\s*(yes|no)\s*\|`)
	goosOf := map[string]string{"Linux": "linux", "macOS": "darwin", "Windows": "windows"}

	var documented []string
	for _, match := range row.FindAllStringSubmatch(doc, -1) {
		for i, goarch := range []string{"amd64", "arm64"} {
			if match[2+i] == "yes" {
				documented = append(documented, goosOf[match[1]]+"/"+goarch)
			}
		}
	}
	sort.Strings(documented)

	if len(documented) == 0 {
		t.Fatal("no release-platform table was found in docs/supported-platforms.md, so this test proves nothing")
	}
	if strings.Join(documented, " ") != strings.Join(releaseTargets, " ") {
		t.Errorf("the documentation promises %v, and this project releases for %v", documented, releaseTargets)
	}
}

// internal/version reads three linker-stamped variables. A release that did
// not set them would report "dev", and `nise doctor` compares the CLI version
// against what a generated project recorded — so an unstamped release makes
// that comparison wrong rather than merely ugly.
func TestTheReleaseStampsTheVersionVariables(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	for _, variable := range []string{"Version", "Commit", "Date"} {
		want := "github.com/drilonrecica/nise-and-go/internal/version." + variable + "="
		if !strings.Contains(source, want) {
			t.Errorf(".goreleaser.yaml does not stamp %s", want)
		}
	}
	// A stamped variable that no longer exists is silently ignored by the
	// linker, so the package has to still declare all three. This is the half
	// of the check that catches a rename in Go rather than in YAML.
	declaration := regexp.MustCompile(`(?m)^\t(Version|Commit|Date)\s+string$`)
	found := declaration.FindAllStringSubmatch(readFile(t, "internal/version/version.go"), -1)
	if len(found) != 3 {
		t.Errorf("internal/version declares %d of the three stamped variables; the release stamps names that may not exist", len(found))
	}
}

// The properties that make a checksum somebody else computes worth anything.
// Verified by building twice and comparing: two runs of `make release-snapshot`
// on one commit produce identical checksums.
func TestTheReleaseIsBuiltReproducibly(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	if !strings.Contains(source, "- -trimpath") {
		t.Error("the release does not pass -trimpath, so the builder's filesystem layout ends up in the binary")
	}
	if !strings.Contains(source, "CGO_ENABLED=0") {
		t.Error("the release does not set CGO_ENABLED=0, so the binaries depend on the builder's system libraries")
	}
	// The commit's timestamp rather than the build's, or two builds of one
	// tag differ and nobody can check anybody else's checksum.
	if !strings.Contains(source, "mod_timestamp:") || !strings.Contains(source, ".CommitTimestamp") {
		t.Error("the release does not pin mod_timestamp to the commit timestamp, so two builds of one tag differ")
	}
}

// A tag pushed by mistake should be a draft to delete, not a version somebody
// has already installed and a module proxy has already cached.
func TestAReleaseIsDraftedRatherThanPublished(t *testing.T) {
	t.Parallel()
	if !regexp.MustCompile(`(?m)^\s*draft:\s*true\s*$`).MatchString(goreleaserConfig(t)) {
		t.Error("the release is published directly; it must be drafted for a human to check first")
	}
}

func TestTheReleaseBuildsTheRealCommand(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	if !strings.Contains(source, "main: ./cmd/nise") {
		t.Error("the release does not build ./cmd/nise")
	}
	if !strings.Contains(source, "binary: nise") {
		t.Error("the release does not name the binary nise")
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), "cmd", "nise")); err != nil {
		t.Errorf("cmd/nise does not exist: %v", err)
	}
}

// The Makefile and the workflow pin GoReleaser separately, and nothing else
// makes them agree — so this does. A release built by a version nobody ran
// locally is a release whose artifacts nobody can reproduce.
func TestTheGoReleaserPinsAgree(t *testing.T) {
	t.Parallel()
	pin := regexp.MustCompile(`GORELEASER_VERSION\s*[:=]+\s*"?(v[0-9][^"\s]*)"?`)

	makefile := pin.FindStringSubmatch(readFile(t, "Makefile"))
	workflow := pin.FindStringSubmatch(readFile(t, ".github/workflows/release.yml"))
	if makefile == nil {
		t.Fatal("the Makefile does not pin GORELEASER_VERSION")
	}
	if workflow == nil {
		t.Fatal(".github/workflows/release.yml does not pin GORELEASER_VERSION")
	}
	if makefile[1] != workflow[1] {
		t.Errorf("the Makefile pins GoReleaser %s and the release workflow pins %s", makefile[1], workflow[1])
	}
}

// A release publishes three different claims about its artifacts, and they
// answer three different questions. Losing any one of them silently is the
// failure this section is here to prevent.

// checksums.txt answers "is this the file that was published".
func TestTheReleasePublishesChecksums(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	if !regexp.MustCompile(`(?m)^checksum:`).MatchString(source) {
		t.Fatal("the release produces no checksum file")
	}
	if !strings.Contains(source, "algorithm: sha256") {
		t.Error("the checksum file does not declare sha256")
	}
	if !strings.Contains(source, `name_template: "checksums.txt"`) {
		t.Error("the checksum file is not named checksums.txt, which is what the documentation tells people to run sha256sum against")
	}
}

// An SBOM answers "what is inside it" for somebody who cannot run this
// project's toolchain. A Go binary already carries its module graph — `go
// version -m` reads it — so the SBOM is not new information; it is that
// information in a format a scanner or a procurement review can read.
func TestTheReleasePublishesAnSBOMPerArchive(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	if !regexp.MustCompile(`(?m)^sboms:`).MatchString(source) {
		t.Fatal("the release produces no software bill of materials")
	}
	if !strings.Contains(source, "artifacts: archive") {
		t.Error("the SBOM is not generated per archive, so the six platforms do not each get one")
	}
	// SPDX rather than CycloneDX: it is the ISO-standardised one, and a
	// project shipping both ships two things to keep consistent.
	if !strings.Contains(source, ".spdx.json") {
		t.Error("the SBOM is not SPDX JSON")
	}
}

// A build attestation answers "who built it", which is the question a
// checksum published beside the artifact by whoever published the artifact
// cannot answer at all.
func TestTheReleaseAttestsWhatItPublishes(t *testing.T) {
	t.Parallel()
	workflow := readFile(t, ".github/workflows/release.yml")

	if !strings.Contains(workflow, "actions/attest-build-provenance@") {
		t.Fatal("the release workflow records no build attestation")
	}
	// The attestation is signed with a short-lived OIDC token minted for the
	// run. Without both permissions the step fails at release time, which is
	// the worst moment to find out.
	for _, permission := range []string{"id-token: write", "attestations: write"} {
		if !strings.Contains(workflow, permission) {
			t.Errorf("the release workflow does not grant %q, so the attestation step cannot run", permission)
		}
	}
	// Every artifact a person can download has to be covered. An archive
	// nobody attested is one somebody can substitute.
	for _, subject := range []string{"dist/*.tar.gz", "dist/*.zip", "dist/*.spdx.json", "dist/checksums.txt"} {
		if !strings.Contains(workflow, subject) {
			t.Errorf("the attestation does not cover %s", subject)
		}
	}
}

// Every action, first-party or not, is pinned by commit — "first-party"
// describes who wrote an action, not what a moved tag would run.
func TestEveryActionIsPinnedByCommit(t *testing.T) {
	t.Parallel()

	workflows, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("listing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows were found, so this test proves nothing")
	}

	uses := regexp.MustCompile(`uses:\s*(\S+)`)
	pinned := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, path := range workflows {
		data, err := os.ReadFile(path) // #nosec G304 -- path came from Glob over this repository.
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, match := range uses.FindAllStringSubmatch(string(data), -1) {
			// A local action is a path into this repository and has nothing
			// to pin.
			if strings.HasPrefix(match[1], "./") {
				continue
			}
			if !pinned.MatchString(match[1]) {
				t.Errorf("%s uses %s, which is not pinned to a full commit SHA", filepath.Base(path), match[1])
			}
		}
	}
}

// The syft pin lives in the workflow only, because nothing local needs it —
// but a missing or malformed one means GoReleaser finds no syft and skips the
// SBOMs, which is a silent omission rather than a failure.
func TestTheSyftPinIsAVersion(t *testing.T) {
	t.Parallel()
	workflow := readFile(t, ".github/workflows/release.yml")

	pin := regexp.MustCompile(`SYFT_VERSION:\s*"(v[0-9]+\.[0-9]+\.[0-9]+)"`)
	if !pin.MatchString(workflow) {
		t.Error("the release workflow does not pin syft to an exact version")
	}
	if !strings.Contains(workflow, "go install github.com/anchore/syft/cmd/syft@${{ env.SYFT_VERSION }}") {
		t.Error("the release workflow does not install syft at the pinned version, so GoReleaser would find none and skip the SBOMs")
	}
}
