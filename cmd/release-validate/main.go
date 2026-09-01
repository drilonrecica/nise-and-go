// Package main validates a published nise release, and the release somebody
// would roll back to.
//
// A release is the one artifact in this project that nothing else checks. CI
// checks the commit, `goreleaser check` checks the configuration, and
// test/release checks that the configuration and the documentation agree —
// but between "goreleaser exited 0" and "somebody downloads a file" there is
// an upload, a draft, a human pressing publish, and a set of release assets
// anybody with write access can add to or delete from. This command reads
// what a stranger would actually download and states whether it is a release.
//
// It answers four questions, in the order that a wrong answer matters:
//
//  1. Is the artifact set exactly right? Six archives, six bills of material,
//     one checksums.txt — no fewer, and no more. A missing archive is a
//     platform whose users cannot install; an unexpected asset is a file
//     somebody uploaded that the release workflow did not build.
//  2. Do the bytes match the published hashes, and does checksums.txt cover
//     every artifact rather than a convenient subset?
//  3. Does each archive contain exactly the binary, LICENSE, and README.md —
//     and is the binary actually executable?
//  4. Does the binary report the version and commit the release claims?
//
// It is hermetic: no network, no toolchain, no subprocess. Everything it
// checks is a file on disk, which is what makes it testable and what makes
// the workflow around it small. Whoever runs it has already fetched the
// assets and verified their build attestation; provenance is not this
// command's job and cannot be done offline.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// releaseTargets are the six platform pairs .goreleaser.yaml builds. They are
// written out rather than read from that file for the same reason
// test/release writes them out: a validator derived from the thing it
// validates agrees with it by construction and proves nothing. test/release
// cross-checks this list against the configuration.
var releaseTargets = []struct{ goos, goarch string }{
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

// releaseTag accepts a release tag, including a prerelease one. Unlike the
// Homebrew generator this command does not refuse prereleases: a prerelease
// is published to GitHub Releases on purpose, and it is exactly the kind of
// release worth validating before anybody opts into it.
var releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// checksumLine is the shape GoReleaser writes: a SHA-256 in lowercase hex,
// two spaces, a filename.
var checksumLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

// archiveContents is what every archive must hold, beside the binary. An
// archive carrying anything else is not the archive this project builds.
var archiveContents = []string{"LICENSE", "README.md"}

// checksumsName is the one asset that is not itself checksummed — nothing
// can vouch for it but the build attestation the caller verified.
const checksumsName = "checksums.txt"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the validator and returns the process exit code, so the whole
// command is testable in-process. Findings go to stdout, because a person
// reading them wants all of them at once rather than the first; the exit code
// is what the workflow reads.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release-validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "release tag the directory is claimed to hold, e.g. v0.1.0")
	dir := fs.String("dir", "", "directory holding every downloaded release asset")
	commit := fs.String("commit", "", "commit the release was built from; checked against the binary's own report")
	report := fs.String("version-report", "", "path to the output of `nise --json version`, run from this release's binary")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	problems, err := validate(*tag, *dir, *commit, *report)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release-validate: %v\n", err)
		return 2
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintf(stdout, "release-validate: %s\n", p)
		}
		noun := "problems"
		if len(problems) == 1 {
			noun = "problem"
		}
		_, _ = fmt.Fprintf(stdout, "release-validate: %s is not a valid release (%d %s)\n", *tag, len(problems), noun)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "release-validate: %s is complete, intact, and reports itself correctly\n", *tag)
	return 0
}

// validate returns the list of problems with the release, or an error when it
// could not look. The distinction matters: an unreadable directory is a
// caller mistake and must not be reported as a bad release, which is the
// failure mode that turns a validator into a rubber stamp.
func validate(tag, dir, commit, reportPath string) ([]string, error) {
	if tag == "" || dir == "" {
		return nil, errors.New("-tag and -dir are both required")
	}
	if !releaseTag.MatchString(tag) {
		return nil, fmt.Errorf("tag %q is not a release tag of the form vMAJOR.MINOR.PATCH[-prerelease]", tag)
	}
	version := strings.TrimPrefix(tag, "v")

	present, err := assetNames(dir)
	if err != nil {
		return nil, err
	}

	var problems []string
	problems = append(problems, checkAssetSet(version, present)...)

	// Everything below reads files. Running it against an incomplete
	// directory would bury the one finding that explains the rest, so the
	// integrity and content checks only run over what is actually there.
	sums, sumProblems := readChecksums(dir, version, present)
	problems = append(problems, sumProblems...)
	problems = append(problems, checkHashes(dir, sums)...)
	problems = append(problems, checkArchives(dir, version, present)...)

	versionProblems, err := checkVersionReport(reportPath, version, commit)
	if err != nil {
		return nil, err
	}
	problems = append(problems, versionProblems...)

	return problems, nil
}

// archiveName is the file GoReleaser's name_template produces. The tag's "v"
// is not part of it: release v0.1.0 ships nise_0.1.0_linux_amd64.tar.gz.
func archiveName(version, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("nise_%s_%s_%s.zip", version, goos, goarch)
	}
	return fmt.Sprintf("nise_%s_%s_%s.tar.gz", version, goos, goarch)
}

// expectedAssets is every file a release publishes: an archive and an SPDX
// document per platform, plus the checksum file covering all twelve.
func expectedAssets(version string) []string {
	names := make([]string, 0, 2*len(releaseTargets)+1)
	for _, target := range releaseTargets {
		archive := archiveName(version, target.goos, target.goarch)
		names = append(names, archive, archive+".spdx.json")
	}
	names = append(names, checksumsName)
	sort.Strings(names)
	return names
}

// assetNames lists the regular files in the asset directory. Subdirectories
// are refused rather than ignored: a release asset is a file, and a directory
// here means the caller downloaded something else as well.
func assetNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the asset directory: %w", err)
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file; -dir must hold the release assets and nothing else",
				filepath.Join(dir, entry.Name()))
		}
		names[entry.Name()] = true
	}
	return names, nil
}

// checkAssetSet compares what is published against what a release publishes,
// in both directions. The second direction is the one people forget: an extra
// asset is a file the release workflow did not build, sitting under the same
// release somebody is being told to trust.
func checkAssetSet(version string, present map[string]bool) []string {
	var problems []string
	expected := expectedAssets(version)
	wanted := make(map[string]bool, len(expected))
	for _, name := range expected {
		wanted[name] = true
		if !present[name] {
			problems = append(problems, fmt.Sprintf("the release is missing %s", name))
		}
	}
	for _, name := range sortedKeys(present) {
		if !wanted[name] {
			problems = append(problems, fmt.Sprintf("the release carries %s, which its workflow does not build", name))
		}
	}
	return problems
}

// readChecksums parses checksums.txt and checks that it names every artifact
// and only those. A checksum file covering a subset lets an unlisted archive
// pass `sha256sum --check` untouched, which is precisely the file somebody
// would replace.
func readChecksums(dir, version string, present map[string]bool) (map[string]string, []string) {
	if !present[checksumsName] {
		return nil, nil // already reported as missing
	}
	path := filepath.Join(dir, checksumsName)
	data, err := os.ReadFile(path) // #nosec G304 -- reading the directory the operator named is what the command is.
	if err != nil {
		return nil, []string{fmt.Sprintf("%s cannot be read: %v", checksumsName, err)}
	}

	var problems []string
	sums := make(map[string]string)
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		match := checksumLine.FindStringSubmatch(line)
		if match == nil {
			problems = append(problems, fmt.Sprintf("%s line %d is not a checksum line: %q", checksumsName, i+1, line))
			continue
		}
		sums[match[2]] = match[1]
	}

	for _, name := range expectedAssets(version) {
		if name == checksumsName {
			continue
		}
		if _, ok := sums[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s does not cover %s", checksumsName, name))
		}
	}
	for _, name := range sortedKeys(sums) {
		if name == checksumsName {
			problems = append(problems, fmt.Sprintf("%s lists itself, which cannot be checked", checksumsName))
			continue
		}
		if !present[name] {
			problems = append(problems, fmt.Sprintf("%s covers %s, which the release does not publish", checksumsName, name))
		}
	}
	return sums, problems
}

// checkHashes recomputes every published hash. This is the check a person
// runs with sha256sum; doing it here means a release whose upload truncated a
// file fails before anybody downloads it rather than after.
func checkHashes(dir string, sums map[string]string) []string {
	var problems []string
	for _, name := range sortedKeys(sums) {
		got, err := fileSHA256(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue // already reported by checkAssetSet
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s cannot be hashed: %v", name, err))
			continue
		}
		if got != sums[name] {
			problems = append(problems, fmt.Sprintf("%s hashes to %s, and %s publishes %s", name, got, checksumsName, sums[name]))
		}
	}
	return problems
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- reading the directory the operator named is what the command is.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// checkArchives opens every archive and compares its entries against the
// three files this project ships. Nothing else looks inside: `goreleaser
// check` validates the configuration's syntax, and an archive that unpacks to
// the wrong thing is discovered by the first person to unpack it.
func checkArchives(dir, version string, present map[string]bool) []string {
	var problems []string
	for _, target := range releaseTargets {
		name := archiveName(version, target.goos, target.goarch)
		if !present[name] {
			continue // already reported as missing
		}
		binary := "nise"
		if target.goos == "windows" {
			binary = "nise.exe"
		}
		entries, err := archiveEntries(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s cannot be read as an archive: %v", name, err))
			continue
		}
		problems = append(problems, compareEntries(name, binary, entries)...)
	}
	return problems
}

// entry is one file inside an archive: its name, its size, and whether its
// recorded mode makes it executable.
type entry struct {
	size       int64
	executable bool
}

func compareEntries(archive, binary string, entries map[string]entry) []string {
	var problems []string
	wanted := append([]string{binary}, archiveContents...)
	for _, name := range wanted {
		found, ok := entries[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s does not contain %s", archive, name))
			continue
		}
		if found.size == 0 {
			problems = append(problems, fmt.Sprintf("%s contains an empty %s", archive, name))
		}
		// Only the binary. LICENSE and README.md are documents, and an
		// archive that marks them executable is odd rather than broken.
		if name == binary && !found.executable {
			problems = append(problems, fmt.Sprintf("%s contains %s without an executable mode; unpacking it produces a file nobody can run", archive, name))
		}
	}
	allowed := make(map[string]bool, len(wanted))
	for _, name := range wanted {
		allowed[name] = true
	}
	for _, name := range sortedKeys(entries) {
		if !allowed[name] {
			problems = append(problems, fmt.Sprintf("%s also contains %s", archive, name))
		}
	}
	return problems
}

func archiveEntries(path string) (map[string]entry, error) {
	if strings.HasSuffix(path, ".zip") {
		return zipEntries(path)
	}
	return tarEntries(path)
}

func tarEntries(path string) (map[string]entry, error) {
	file, err := os.Open(path) // #nosec G304 -- reading the directory the operator named is what the command is.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	entries := make(map[string]entry)
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("tar: %s is not a regular file", header.Name)
		}
		entries[header.Name] = entry{
			size:       header.Size,
			executable: header.FileInfo().Mode().Perm()&0o111 != 0,
		}
	}
}

func zipEntries(path string) (map[string]entry, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	entries := make(map[string]entry)
	for _, file := range reader.File {
		info := file.FileInfo()
		if info.IsDir() {
			return nil, fmt.Errorf("zip: %s is a directory", file.Name)
		}
		// A zip written on Windows carries no Unix mode at all, so the
		// executable bit is not a property this format always records. The
		// release is built on Linux, where GoReleaser does record it — and a
		// zip that suddenly stopped recording it means the build changed.
		entries[file.Name] = entry{
			size:       info.Size(),
			executable: info.Mode().Perm()&0o111 != 0,
		}
	}
	return entries, nil
}

// checkVersionReport compares `nise --json version`, run from the archive
// this release published, against what the release claims to be. This is the
// only check that says anything about the binary rather than about the files
// around it: an archive can be complete, correctly hashed, and contain a
// binary built from the wrong commit or stamped with no version at all.
func checkVersionReport(path, version, commit string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- reading the path the operator named is what the command is.
	if err != nil {
		return nil, fmt.Errorf("reading the version report: %w", err)
	}
	var reported struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal(data, &reported); err != nil {
		return nil, fmt.Errorf("parsing the version report: %w", err)
	}

	var problems []string
	// GoReleaser stamps {{ .Version }}, which is the tag without its "v".
	if reported.Version != version {
		problems = append(problems, fmt.Sprintf("the binary reports version %q, and the release is %s", reported.Version, version))
	}
	if commit != "" && reported.Commit != commit {
		problems = append(problems, fmt.Sprintf("the binary reports commit %q, and the release was built from %s", reported.Commit, commit))
	}
	if reported.Date == "" {
		problems = append(problems, "the binary reports no build date, so its ldflags were not applied")
	}
	return problems, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
