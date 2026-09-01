package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generateToTemp runs the generator against the shared fixture checksums and
// returns the produced bytes, failing the test on any error.
func generateToTemp(t *testing.T, tag string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "nise.rb")
	if err := generate(tag, filepath.Join("testdata", "checksums.txt"), out, false); err != nil {
		t.Fatalf("generate: %v", err)
	}
	data, err := os.ReadFile(out) // #nosec G304 -- out is under this test's TempDir.
	if err != nil {
		t.Fatalf("reading the generated formula: %v", err)
	}
	return data
}

// The golden file is what the tap will actually serve. A change to the
// template shows up here as a reviewable diff instead of appearing for the
// first time in the tap repository after a release.
func TestGeneratesTheGoldenFormula(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(filepath.Join("testdata", "nise.rb"))
	if err != nil {
		t.Fatalf("reading the golden formula: %v", err)
	}
	got := generateToTemp(t, "v0.1.0")
	if string(got) != string(golden) {
		t.Errorf("the generated formula differs from testdata/nise.rb:\n--- got ---\n%s\n--- golden ---\n%s", got, golden)
	}
}

// Same inputs, same bytes. The formula is committed to the tap, so a
// nondeterministic byte would show up as a phantom diff on every release.
func TestTwoRunsProduceIdenticalBytes(t *testing.T) {
	t.Parallel()
	first := generateToTemp(t, "v0.1.0")
	second := generateToTemp(t, "v0.1.0")
	if string(first) != string(second) {
		t.Error("two runs over identical inputs produced different bytes")
	}
	if !strings.HasSuffix(string(first), "end\n") || strings.HasSuffix(string(first), "\n\n") {
		t.Error("the formula does not end with exactly one trailing newline")
	}
}

// The formula covers exactly the platforms Homebrew runs on: the release
// pairs minus Windows. Four url stanzas, none of them a zip.
func TestTheFormulaCoversTheNonWindowsReleasePairs(t *testing.T) {
	t.Parallel()
	formula := string(generateToTemp(t, "v0.1.0"))

	for _, name := range []string{
		"nise_0.1.0_darwin_amd64.tar.gz",
		"nise_0.1.0_darwin_arm64.tar.gz",
		"nise_0.1.0_linux_amd64.tar.gz",
		"nise_0.1.0_linux_arm64.tar.gz",
	} {
		if !strings.Contains(formula, "/releases/download/v0.1.0/"+name) {
			t.Errorf("the formula does not install %s", name)
		}
	}
	if got := strings.Count(formula, "url \""); got != 4 {
		t.Errorf("the formula names %d archives; Homebrew installs on exactly 4 platforms", got)
	}
	if strings.Contains(formula, "windows") {
		t.Error("the formula mentions windows, which Homebrew does not run on")
	}
}

// Prereleases and malformed tags never reach the tap, whatever the workflow
// that invoked the generator believed.
func TestRefusesATagThatIsNotAPlainRelease(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"0.1.0", "v0.1", "v0.1.0-rc.1", "v0.1.0+build", "v01.0.0", ""} {
		out := filepath.Join(t.TempDir(), "nise.rb")
		if err := generate(tag, filepath.Join("testdata", "checksums.txt"), out, false); err == nil {
			t.Errorf("tag %q was accepted; only plain vMAJOR.MINOR.PATCH tags may reach the tap", tag)
		}
	}
}

// A checksums file missing one of the four archives means a formula that
// breaks installation for that platform's users, so it is refused rather
// than partially written.
func TestRefusesAChecksumsFileMissingABrewArchive(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile(filepath.Join("testdata", "checksums.txt"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var kept []string
	for line := range strings.SplitSeq(strings.TrimRight(string(fixture), "\n"), "\n") {
		if !strings.HasSuffix(line, "nise_0.1.0_linux_arm64.tar.gz") {
			kept = append(kept, line)
		}
	}
	dir := t.TempDir()
	truncated := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(truncated, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing the truncated fixture: %v", err)
	}

	err = generate("v0.1.0", truncated, filepath.Join(dir, "nise.rb"), false)
	if err == nil || !strings.Contains(err.Error(), "nise_0.1.0_linux_arm64.tar.gz") {
		t.Errorf("a checksums file missing the linux/arm64 archive produced %v; it must be refused by name", err)
	}
}

// A line that does not parse means this is not the file GoReleaser
// published, and embedding hashes from it would be embedding hashes from
// something else.
func TestRefusesAMalformedChecksumLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "checksums.txt")
	content := "deadbeef  nise_0.1.0_darwin_amd64.tar.gz\n" // 8 hex characters, not 64
	if err := os.WriteFile(bad, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the malformed fixture: %v", err)
	}
	if err := generate("v0.1.0", bad, filepath.Join(dir, "nise.rb"), false); err == nil {
		t.Error("a malformed checksum line was accepted")
	}
}

// Publishing an old release again must not silently move every
// `brew upgrade` user backwards. Rolling back is explicit.
func TestRefusesToDowngradeTheTap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "nise.rb")
	newer := "class Nise < Formula\n  version \"0.2.0\"\nend\n"
	if err := os.WriteFile(out, []byte(newer), 0o600); err != nil {
		t.Fatalf("writing the existing formula: %v", err)
	}

	err := generate("v0.1.0", filepath.Join("testdata", "checksums.txt"), out, false)
	if err == nil || !strings.Contains(err.Error(), "-allow-downgrade") {
		t.Errorf("writing 0.1.0 over 0.2.0 produced %v; it must be refused with the flag that overrides it", err)
	}

	if err := generate("v0.1.0", filepath.Join("testdata", "checksums.txt"), out, true); err != nil {
		t.Errorf("-allow-downgrade did not permit the rollback: %v", err)
	}
}

// Re-running against the version the tap already serves is how the workflow
// is retried; it must succeed and change nothing.
func TestRewritingTheSameVersionIsIdempotent(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "nise.rb")
	for i := range 2 {
		if err := generate("v0.1.0", filepath.Join("testdata", "checksums.txt"), out, false); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

// A file at -out that this generator did not write is somebody else's file.
func TestRefusesToOverwriteAFileItDoesNotOwn(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "nise.rb")
	if err := os.WriteFile(out, []byte("# somebody's hand-written formula\n"), 0o600); err != nil {
		t.Fatalf("writing the foreign file: %v", err)
	}
	if err := generate("v0.1.0", filepath.Join("testdata", "checksums.txt"), out, false); err == nil {
		t.Error("a file without this generator's version stanza was overwritten")
	}
}

// run wires flags to generate; the exit codes are what the workflow's shell
// sees, so they are part of the contract.
func TestRunExitCodes(t *testing.T) {
	t.Parallel()
	var stderr strings.Builder
	out := filepath.Join(t.TempDir(), "nise.rb")

	if code := run([]string{"-tag", "v0.1.0", "-checksums", filepath.Join("testdata", "checksums.txt"), "-out", out}, &stderr); code != 0 {
		t.Errorf("a valid invocation exited %d: %s", code, stderr.String())
	}
	if code := run([]string{"-tag", "v0.1.0-rc.1", "-checksums", filepath.Join("testdata", "checksums.txt"), "-out", out}, &stderr); code != 1 {
		t.Errorf("a prerelease tag exited %d, want 1", code)
	}
	if code := run([]string{"-no-such-flag"}, &stderr); code != 2 {
		t.Errorf("an unknown flag exited %d, want 2", code)
	}
}
