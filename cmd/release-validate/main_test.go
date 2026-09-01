package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests build a release rather than committing one. A fixture release
// would be twelve binary files nobody could review, and every interesting
// case here is a *deviation* from a correct release — so the correct release
// has to be constructible for the deviations to be one line each.

const testVersion = "0.1.0"

const testCommit = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// release is a directory of assets under construction.
type release struct {
	t     *testing.T
	dir   string
	files map[string][]byte
}

func newRelease(t *testing.T) *release {
	t.Helper()
	r := &release{t: t, dir: t.TempDir(), files: map[string][]byte{}}
	for _, target := range releaseTargets {
		name := archiveName(testVersion, target.goos, target.goarch)
		binary := "nise"
		if target.goos == "windows" {
			binary = "nise.exe"
		}
		r.files[name] = buildArchive(t, name, map[string][]byte{
			binary:      []byte("\x7fELF not really, but not empty either"),
			"LICENSE":   []byte("MIT\n"),
			"README.md": []byte("# Nise & Go\n"),
		}, binary)
		r.files[name+".spdx.json"] = []byte(`{"spdxVersion":"SPDX-2.3"}` + "\n")
	}
	return r
}

// write lays the release out on disk, computing checksums.txt last so that it
// always describes whatever the test just did.
func (r *release) write() string {
	r.t.Helper()
	var lines []string
	for _, name := range sortedKeys(r.files) {
		if err := os.WriteFile(filepath.Join(r.dir, name), r.files[name], 0o600); err != nil {
			r.t.Fatalf("writing %s: %v", name, err)
		}
		sum := sha256.Sum256(r.files[name])
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
	}
	sort.Strings(lines)
	r.writeChecksums(strings.Join(lines, "\n") + "\n")
	return r.dir
}

func (r *release) writeChecksums(content string) {
	r.t.Helper()
	// #nosec G703 -- r.dir is this test's own t.TempDir(); no input reaches it.
	if err := os.WriteFile(filepath.Join(r.dir, checksumsName), []byte(content), 0o600); err != nil {
		r.t.Fatalf("writing %s: %v", checksumsName, err)
	}
}

func buildArchive(t *testing.T, name string, contents map[string][]byte, binary string) []byte {
	t.Helper()
	if strings.HasSuffix(name, ".zip") {
		return buildZip(t, contents, binary)
	}
	return buildTarGz(t, contents, binary)
}

func mode(name, binary string) int64 {
	if name == binary {
		return 0o755
	}
	return 0o644
}

func buildTarGz(t *testing.T, contents map[string][]byte, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(contents) {
		body := contents[name]
		err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode(name, binary), Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		if err != nil {
			t.Fatalf("tar header for %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar body for %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, contents map[string][]byte, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range sortedKeys(contents) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(os.FileMode(mode(name, binary))) // #nosec G115 -- a literal permission constant.
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("zip header for %s: %v", name, err)
		}
		if _, err := w.Write(contents[name]); err != nil {
			t.Fatalf("zip body for %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func versionReport(t *testing.T, version, commit, date string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "version.json")
	body := fmt.Sprintf(`{"version":%q,"commit":%q,"date":%q}`, version, commit, date)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the version report: %v", err)
	}
	return path
}

// check runs the validator over a release and returns its findings. A
// validator that could not look fails the test rather than returning no
// findings, which is the distinction the command exists to keep.
func check(t *testing.T, dir string, extra ...string) []string {
	t.Helper()
	args := append([]string{"-tag", "v" + testVersion, "-dir", dir}, extra...)
	problems, err := validate(argValue(args, "-tag"), argValue(args, "-dir"),
		argValue(args, "-commit"), argValue(args, "-version-report"))
	if err != nil {
		t.Fatalf("the validator could not look: %v", err)
	}
	return problems
}

func argValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func mustFind(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p, want) {
			return
		}
	}
	t.Errorf("no problem mentioned %q; the validator reported %v", want, problems)
}

func TestACorrectReleaseValidates(t *testing.T) {
	t.Parallel()
	dir := newRelease(t).write()

	problems := check(t, dir,
		"-version-report", versionReport(t, testVersion, testCommit, "2026-09-01T00:00:00Z"),
		"-commit", testCommit)
	if len(problems) != 0 {
		t.Fatalf("a correct release was reported as broken: %v", problems)
	}
}

// All six platforms are checked, not just the first. A validator that stops
// at the first archive silently blesses the other five.
func TestEveryPlatformIsChecked(t *testing.T) {
	t.Parallel()
	for _, target := range releaseTargets {
		t.Run(target.goos+"/"+target.goarch, func(t *testing.T) {
			t.Parallel()
			r := newRelease(t)
			name := archiveName(testVersion, target.goos, target.goarch)
			delete(r.files, name)
			mustFind(t, check(t, r.write()), "the release is missing "+name)
		})
	}
}

func TestAMissingBillOfMaterialsIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	name := archiveName(testVersion, "linux", "amd64") + ".spdx.json"
	delete(r.files, name)
	mustFind(t, check(t, r.write()), "the release is missing "+name)
}

// The direction people forget: a release asset anybody with write access can
// add, sitting under the release everybody is told to trust.
func TestAnUnexpectedAssetIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	r.files["nise_0.1.0_linux_amd64.deb"] = []byte("not built here")
	mustFind(t, check(t, r.write()), "which its workflow does not build")
}

func TestATamperedArchiveFailsItsChecksum(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	dir := r.write()

	name := archiveName(testVersion, "linux", "amd64")
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path) // #nosec G304 -- a path this test just wrote.
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	// #nosec G703 -- path is under this test's own t.TempDir().
	if err := os.WriteFile(path, append(data, 'x'), 0o600); err != nil {
		t.Fatalf("appending to %s: %v", name, err)
	}
	mustFind(t, check(t, dir), name+" hashes to ")
}

// A checksum file covering eleven of twelve leaves the twelfth free to be
// replaced, and `sha256sum --check` reports success either way.
func TestAChecksumFileThatSkipsAnArtifactIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	dir := r.write()

	skipped := archiveName(testVersion, "windows", "arm64")
	data, err := os.ReadFile(filepath.Join(dir, checksumsName)) // #nosec G304 -- a path this test just wrote.
	if err != nil {
		t.Fatalf("reading %s: %v", checksumsName, err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.HasSuffix(line, "  "+skipped) {
			kept = append(kept, line)
		}
	}
	r.writeChecksums(strings.Join(kept, "\n") + "\n")
	mustFind(t, check(t, dir), checksumsName+" does not cover "+skipped)
}

func TestAChecksumFileNamingSomethingUnpublishedIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	dir := r.write()

	data, err := os.ReadFile(filepath.Join(dir, checksumsName)) // #nosec G304 -- a path this test just wrote.
	if err != nil {
		t.Fatalf("reading %s: %v", checksumsName, err)
	}
	r.writeChecksums(string(data) + strings.Repeat("0", 64) + "  nise_0.1.0_plan9_amd64.tar.gz\n")
	mustFind(t, check(t, dir), "which the release does not publish")
}

func TestAMalformedChecksumLineIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	dir := r.write()
	r.writeChecksums("this is not a checksum line\n")
	mustFind(t, check(t, dir), "is not a checksum line")
}

func TestAnArchiveMissingItsLicenceIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	name := archiveName(testVersion, "linux", "arm64")
	r.files[name] = buildArchive(t, name, map[string][]byte{
		"nise":      []byte("binary"),
		"README.md": []byte("# Nise & Go\n"),
	}, "nise")
	mustFind(t, check(t, r.write()), name+" does not contain LICENSE")
}

func TestAnArchiveCarryingSomethingElseIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	name := archiveName(testVersion, "darwin", "arm64")
	r.files[name] = buildArchive(t, name, map[string][]byte{
		"nise":      []byte("binary"),
		"LICENSE":   []byte("MIT\n"),
		"README.md": []byte("# Nise & Go\n"),
		".env":      []byte("SECRET=hunter2\n"),
	}, "nise")
	mustFind(t, check(t, r.write()), name+" also contains .env")
}

// An archive whose binary unpacks without an executable bit installs a file
// nobody can run, and every other check here passes on it.
func TestABinaryWithoutAnExecutableModeIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	name := archiveName(testVersion, "linux", "amd64")
	r.files[name] = buildArchive(t, name, map[string][]byte{
		"nise":      []byte("binary"),
		"LICENSE":   []byte("MIT\n"),
		"README.md": []byte("# Nise & Go\n"),
	}, "not-the-binary")
	mustFind(t, check(t, r.write()), "without an executable mode")
}

func TestAnEmptyBinaryIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	name := archiveName(testVersion, "windows", "amd64")
	r.files[name] = buildArchive(t, name, map[string][]byte{
		"nise.exe":  {},
		"LICENSE":   []byte("MIT\n"),
		"README.md": []byte("# Nise & Go\n"),
	}, "nise.exe")
	mustFind(t, check(t, r.write()), "contains an empty nise.exe")
}

func TestAnArchiveThatIsNotAnArchiveIsReported(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	r.files[archiveName(testVersion, "linux", "amd64")] = []byte("404: Not Found\n")
	mustFind(t, check(t, r.write()), "cannot be read as an archive")
}

// The binary is the one thing the surrounding files cannot vouch for: a
// complete, correctly hashed release can still ship a binary built from the
// wrong commit, or with no ldflags at all.
func TestTheBinaryMustReportTheReleaseItIsIn(t *testing.T) {
	t.Parallel()
	dir := newRelease(t).write()

	t.Run("wrong version", func(t *testing.T) {
		t.Parallel()
		report := versionReport(t, "0.0.9", testCommit, "2026-09-01T00:00:00Z")
		mustFind(t, check(t, dir, "-version-report", report), `reports version "0.0.9"`)
	})
	t.Run("wrong commit", func(t *testing.T) {
		t.Parallel()
		report := versionReport(t, testVersion, strings.Repeat("f", 40), "2026-09-01T00:00:00Z")
		mustFind(t, check(t, dir, "-version-report", report, "-commit", testCommit), "and the release was built from")
	})
	t.Run("unstamped", func(t *testing.T) {
		t.Parallel()
		report := versionReport(t, "dev", "", "")
		mustFind(t, check(t, dir, "-version-report", report), "its ldflags were not applied")
	})
}

// The failure that turns a validator into a rubber stamp: it could not look,
// and reported nothing wrong. Every "could not look" is exit 2.
func TestBeingUnableToLookIsNotSuccess(t *testing.T) {
	t.Parallel()
	dir := newRelease(t).write()

	cases := []struct {
		name string
		args []string
	}{
		{"no tag", []string{"-dir", dir}},
		{"no directory", []string{"-tag", "v" + testVersion}},
		{"tag is not a release tag", []string{"-tag", "0.1.0", "-dir", dir}},
		{"tag is a branch name", []string{"-tag", "main", "-dir", dir}},
		{"directory does not exist", []string{"-tag", "v" + testVersion, "-dir", filepath.Join(dir, "nowhere")}},
		{"version report does not exist", []string{"-tag", "v" + testVersion, "-dir", dir, "-version-report", filepath.Join(dir, "nowhere.json")}},
		{"version report is not JSON", []string{"-tag", "v" + testVersion, "-dir", dir, "-version-report", filepath.Join(dir, checksumsName)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr); code != 2 {
				t.Errorf("exit code %d; a validator that could not look must not exit 0 or 1 (stdout %q)", code, stdout.String())
			}
		})
	}
}

// A directory among the assets means the caller downloaded something else as
// well, and the asset set this command compares against is then not the
// release's.
func TestADirectoryAmongTheAssetsIsRefused(t *testing.T) {
	t.Parallel()
	dir := newRelease(t).write()
	if err := os.Mkdir(filepath.Join(dir, "extracted"), 0o750); err != nil {
		t.Fatalf("creating a subdirectory: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-tag", "v" + testVersion, "-dir", dir}, &stdout, &stderr); code != 2 {
		t.Errorf("exit code %d; a directory among the assets must be refused", code)
	}
}

// A prerelease is published on purpose, for people who opted in by reading
// the releases page. It is exactly the kind of release worth validating —
// unlike the Homebrew generator, which refuses one.
func TestAPrereleaseTagIsValidated(t *testing.T) {
	t.Parallel()
	if !releaseTag.MatchString("v0.2.0-rc.1") {
		t.Error("v0.2.0-rc.1 is a release tag this command must be able to validate")
	}
}

func TestTheExitCodesAreDistinct(t *testing.T) {
	t.Parallel()
	good := newRelease(t).write()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-tag", "v" + testVersion, "-dir", good}, &stdout, &stderr); code != 0 {
		t.Fatalf("a correct release exited %d: %s", code, stdout.String())
	}

	broken := newRelease(t)
	delete(broken.files, archiveName(testVersion, "linux", "amd64"))
	stdout.Reset()
	if code := run([]string{"-tag", "v" + testVersion, "-dir", broken.write()}, &stdout, &stderr); code != 1 {
		t.Errorf("a broken release exited %d, and a broken release is exit 1", code)
	}
	if !strings.Contains(stdout.String(), "is not a valid release") {
		t.Errorf("the summary line is missing from %q", stdout.String())
	}
}
