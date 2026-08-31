package golden_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// TestGenerationIsStableAcrossWorkingDirectories is the determinism gate this
// suite adds over a plain byte comparison: the same options generate the same
// tree from two unrelated working directories.
//
// The working directory is the most likely channel for an accidental
// environment dependency — a template that interpolated a resolved path, or a
// generator that read something relative — and it is invisible to a test that
// runs everything from the package directory. This one chdirs deliberately,
// which is process-wide state, so it does not run in parallel.
func TestGenerationIsStableAcrossWorkingDirectories(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			first := generateFrom(t, t.TempDir(), v)
			second := generateFrom(t, t.TempDir(), v)

			if first != second {
				t.Errorf("two runs from different working directories produced different trees:\n--- first ---\n%s\n--- second ---\n%s", first, second)
			}
		})
	}
}

// generateFrom generates one variant with wd as the process working
// directory, into a directory unrelated to wd, and returns its manifest.
func generateFrom(t *testing.T, wd string, v variant) string {
	t.Helper()

	t.Chdir(wd)
	root := filepath.Join(t.TempDir(), v.Options.Name)
	if _, err := generator.Write(root, v.Options); err != nil {
		t.Fatalf("generating from %s: %v", wd, err)
	}
	return treeManifest(t, root, v.Name)
}

// timestampPattern matches the shapes a build stamp arrives in: an RFC 3339
// datetime, and a bare ISO date.
var timestampPattern = regexp.MustCompile(`\b(19|20)\d\d-\d\d-\d\d(?:[T ]\d\d:\d\d:\d\d)?\b`)

// TestGeneratedTreeCarriesNoEnvironmentTraces is the direct check on the
// determinism rule's named prohibitions: no absolute path from this machine,
// no username, no timestamp, no CRLF.
//
// "No absolute path" is enforced as "no absolute path derived from this
// machine", which is the version that is actually meaningful. A generated
// tree legitimately contains absolute paths that are not environment-derived
// at all — the Dockerfile's WORKDIR, and the deliberate
// "/path/to/your/nise-and-go" placeholder in the replace-directive advice —
// so a literal ban on the "/" character would either be false or would have
// to carve out so many exceptions that it stopped meaning anything. What
// must never appear is a path that came from whoever ran the generator.
func TestGeneratedTreeCarriesNoEnvironmentTraces(t *testing.T) {
	adoptSyntheticIdentity(t)

	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			root := generate(t, v)

			for _, rel := range treePaths(t, root) {
				body := readTreeFile(t, root, rel)

				// Line endings. A contributor cloning this repository on
				// Windows with core.autocrlf enabled gets CRLF template
				// files; without normalization every generated project on
				// that machine would differ from every other machine's.
				if strings.Contains(body, "\r") {
					t.Errorf("%s contains a carriage return", rel)
				}

				for _, needle := range environmentStrings(t, root) {
					if strings.Contains(body, needle) {
						t.Errorf("%s contains the environment-derived string %q", rel, needle)
					}
				}

				if m := timestampPattern.FindString(body); m != "" {
					t.Errorf("%s contains what looks like a timestamp: %q", rel, m)
				}

				if username := currentUsername(); username != "" {
					if strings.Contains(body, username) {
						t.Errorf("%s contains the current user's name %q", rel, username)
					}
				}
			}
		})
	}
}

// environmentStrings returns the machine-specific paths that must not appear
// anywhere in a generated tree: the directory generation wrote into, the
// process working directory, the user's home directory, and the Windows
// user-profile prefix.
func environmentStrings(t *testing.T, root string) []string {
	t.Helper()

	out := []string{root, `C:\Users\`}
	if wd, err := os.Getwd(); err == nil && wd != "" && wd != string(filepath.Separator) {
		out = append(out, wd)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != string(filepath.Separator) {
		out = append(out, home)
	}
	if tmp := os.TempDir(); tmp != "" && tmp != string(filepath.Separator) {
		out = append(out, tmp)
	}
	return out
}

// syntheticUsername is the account name this test runs under. It is a
// string no template, dependency name, or piece of English prose will ever
// contain, which is the entire reason it exists.
const syntheticUsername = "nise-golden-identity-probe"

// adoptSyntheticIdentity points every environment variable that names the
// current account at a directory called syntheticUsername, for the duration
// of the test.
//
// Searching a generated tree for the *real* account name only works while
// that name is not also an ordinary word, and it is not a safe assumption:
// GitHub's hosted runners execute as an account literally named "runner",
// which appears legitimately and unavoidably in generated content — a test
// runner, a migration runner, a job runner. The check reported a leak on
// thirteen files on every hosted platform, none of them a leak.
//
// Lengthening the minimum name, or excluding a list of English words, only
// moves the collision. Choosing the identity instead removes it: generation
// runs under a name that cannot occur by coincidence, so any occurrence is
// a leak and every occurrence is reported. It is also the stronger test,
// because it holds identically on the maintainer's machine, on a hosted
// runner, and in a container running as "root" — three environments where
// the previous check was respectively meaningful, wrong, and silently
// disabled by the four-character floor.
//
// What this cannot see is an identity read by a route that ignores the
// environment entirely: os/user compiled with cgo consults the passwd
// database directly. That is a deliberate limit, and the same one the
// previous implementation had — it read the environment too, via
// os.UserHomeDir.
func adoptSyntheticIdentity(t *testing.T) {
	t.Helper()

	home := filepath.Join(t.TempDir(), syntheticUsername)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("creating the synthetic home directory: %v", err)
	}
	// HOME is what os.UserHomeDir reads on Unix, USERPROFILE on Windows.
	// USER, LOGNAME, and USERNAME are the three spellings a template or a
	// dependency would reach for to name the account without asking for a
	// home directory at all.
	for _, name := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"USER", "LOGNAME", "USERNAME"} {
		t.Setenv(name, syntheticUsername)
	}
}

// currentUsername returns the account name to search for. os/user is
// deliberately not used: it can require cgo, and the home directory's base
// name is the same answer on every platform this suite runs on.
//
// It returns "" rather than a name when the environment has not been
// adopted — see adoptSyntheticIdentity — because a real account name is
// exactly what this check cannot search for.
func currentUsername() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	name := filepath.Base(home)
	if name != syntheticUsername {
		return ""
	}
	return name
}

// treePaths returns every file in a generated tree, relative and
// slash-separated, sorted. Paths are collected during the walk and read
// afterwards by the caller, so no filesystem operation happens inside the
// walk callback.
func treePaths(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}
