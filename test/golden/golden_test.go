package golden_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// fixedVersion is the CLI version every variant is generated with. The recipe
// records the generating CLI's version, which changes on every release, so a
// golden suite has to inject one rather than read the build's own (ADR 0010).
const fixedVersion = "v0.0.0-golden"

// fixedName and fixedModulePath are the application identity every variant is
// generated with. They are literals, never derived from the environment: a
// golden tree that embedded the machine's own user or directory would differ
// on every contributor's checkout.
const (
	fixedName       = "myapp"
	fixedModulePath = "github.com/example/myapp"
)

// updateEnv is the environment variable that rewrites the committed goldens.
// See this package's doc comment for why it is an environment variable and
// not a -update flag.
const updateEnv = "UPDATE_GOLDEN"

// variant is one committed recipe shape.
type variant struct {
	// Name is the testdata subdirectory and the subtest name.
	Name string
	// Options are the generation inputs.
	Options generator.Options
}

// variants are the recipe shapes this suite pins. Two are enough to cover the
// axis that actually changes the tree: the default recipe, and every optional
// module selected at once. A variant per individual module would multiply the
// committed output without exercising a distinct code path — module selection
// is a sorted list rendered into one file.
func variants() []variant {
	base := generator.Options{
		Name:       fixedName,
		ModulePath: fixedModulePath,
		CLIVersion: fixedVersion,
	}
	all := base
	all.Modules = recipe.Modules()

	return []variant{
		{Name: "default", Options: base},
		{Name: "all-modules", Options: all},
	}
}

// contentPinnedAppOwned is category 2 of the content rule in this package's
// doc comment: application-owned files whose content encodes a security,
// dependency, deployment, or configuration-contract decision. Category 1 —
// every nise-owned file — is derived from the plan rather than listed, so it
// cannot fall out of date.
var contentPinnedAppOwned = []string{
	".dockerignore",
	".env.example",
	".gitignore",
	"deploy/Dockerfile",
	"deploy/compose.yaml",
	"frontend/package.json",
	"go.mod",
	"internal/platform/config/config.go",
	"internal/platform/httpapi/router.go",
}

// updating reports whether this run rewrites the committed goldens.
func updating() bool { return os.Getenv(updateEnv) != "" }

// generate writes one variant into a fresh temporary directory and returns
// its root.
func generate(t *testing.T, v variant) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), v.Options.Name)
	if _, err := generator.Write(root, v.Options); err != nil {
		t.Fatalf("generating the %s variant: %v", v.Name, err)
	}
	return root
}

// contentPinned returns every path whose full contents are committed for a
// variant, sorted: every nise-owned file in the plan, plus the
// application-owned files listed above.
func contentPinned(t *testing.T, v variant) []string {
	t.Helper()

	files, err := generator.Plan(v.Options)
	if err != nil {
		t.Fatalf("planning the %s variant: %v", v.Name, err)
	}

	paths := slices.Clone(contentPinnedAppOwned)
	for _, f := range files {
		if f.Owner == generator.OwnerNise {
			paths = append(paths, f.Path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// TestGoldenManifestIsCurrent is the staleness gate: a generated tree that no
// longer matches its committed manifest fails, naming the file to review.
func TestGoldenManifestIsCurrent(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			got := treeManifest(t, generate(t, v), v.Name)
			manifestPath := filepath.Join("testdata", v.Name, "manifest.txt")

			if updating() {
				writeGolden(t, manifestPath, got)
				return
			}

			want, err := os.ReadFile(manifestPath) // #nosec G304 -- a path built from this test's own literals.
			if err != nil {
				t.Fatalf("reading %s (run with %s=1 to create it): %v", manifestPath, updateEnv, err)
			}
			assertManifestsMatch(t, manifestPath, got, string(want))
		})
	}
}

// TestGoldenContentIsCurrent compares the committed full contents, and also
// fails on a golden file left behind for a path the generator no longer
// produces — a stale golden that matches nothing is a golden nobody notices
// is wrong.
func TestGoldenContentIsCurrent(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			root := generate(t, v)
			pinned := contentPinned(t, v)
			contentDir := filepath.Join("testdata", v.Name, "content")

			if updating() {
				// Rewriting the directory wholesale is what removes a
				// golden for a file that is no longer generated.
				if err := os.RemoveAll(contentDir); err != nil {
					t.Fatalf("clearing %s: %v", contentDir, err)
				}
				for _, rel := range pinned {
					writeGolden(t, filepath.Join(contentDir, goldenName(rel)), readTreeFile(t, root, rel))
				}
				return
			}

			for _, rel := range pinned {
				goldenPath := filepath.Join(contentDir, goldenName(rel))
				want, err := os.ReadFile(goldenPath) // #nosec G304 -- a path built from this test's own literals and the generator's own manifest.
				if err != nil {
					t.Errorf("%s has no committed golden (run with %s=1 to create it): %v", rel, updateEnv, err)
					continue
				}
				if got := readTreeFile(t, root, rel); got != string(want) {
					t.Errorf("%s differs from %s.\n--- generated ---\n%s\n--- committed ---\n%s\nRun `%s=1 go test ./test/golden/...` and review the diff if the change was intended.",
						rel, goldenPath, got, string(want), updateEnv)
				}
			}

			assertNoStaleGoldens(t, contentDir, pinned)
		})
	}
}

// TestContentGoldensCoverEveryNiseOwnedFile makes category 1 of the content
// rule structural rather than a promise. A new nise-owned template that
// landed without a committed golden would be a file users are told never to
// edit, whose contents no review ever sees.
func TestContentGoldensCoverEveryNiseOwnedFile(t *testing.T) {
	t.Parallel()

	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()

			files, err := generator.Plan(v.Options)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			pinned := contentPinned(t, v)

			owned := 0
			for _, f := range files {
				if f.Owner != generator.OwnerNise {
					continue
				}
				owned++
				if !slices.Contains(pinned, f.Path) {
					t.Errorf("nise-owned %s has no committed content golden", f.Path)
				}
			}
			if owned == 0 {
				t.Fatal("the plan contains no nise-owned files, so this test proves nothing")
			}
		})
	}
}

// assertNoStaleGoldens fails when the content directory holds a golden for a
// path the generator no longer produces.
func assertNoStaleGoldens(t *testing.T, contentDir string, pinned []string) {
	t.Helper()

	expected := make(map[string]struct{}, len(pinned))
	for _, rel := range pinned {
		expected[goldenName(rel)] = struct{}{}
	}

	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatalf("reading %s: %v", contentDir, err)
	}
	for _, e := range entries {
		if _, ok := expected[e.Name()]; !ok {
			t.Errorf("%s is a golden for a file the generator no longer produces; run `%s=1 go test ./test/golden/...`",
				filepath.Join(contentDir, e.Name()), updateEnv)
		}
	}
}

// goldenName maps a generated path to the flat golden filename it is stored
// under.
//
// The mapping exists because a mirrored directory tree under testdata would
// put real .gitignore and .env.example files into this repository, where git
// applies them: this repository's own .gitignore excludes ".env.*" (with an
// exception for exactly ".env.example"), so a mirrored ".env.example.golden"
// would be silently untracked, and a mirrored ".gitignore" would actually
// govern the directory holding it. Flattening the path and prefixing the
// leading dot avoids both, and keeps every golden in one directory that
// `ls` shows at a glance.
func goldenName(rel string) string {
	name := strings.ReplaceAll(rel, "/", "__")
	if after, hadDot := strings.CutPrefix(name, "."); hadDot {
		name = "dot." + after
	}
	return name + ".golden"
}

// writeGolden creates a golden file and the directories above it.
func writeGolden(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// readTreeFile reads one file out of a generated tree.
func readTreeFile(t *testing.T, root, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) // #nosec G304 -- root is this test's own t.TempDir() and rel comes from the generator's own manifest.
	if err != nil {
		t.Fatalf("reading %s from the generated tree: %v", rel, err)
	}
	return string(data)
}

// treeManifest renders a generated tree as the committed manifest format:
// a header, then one line per entry, sorted by path.
//
//	<path> dir  <mode>
//	<path> file <mode> <sha256>
//
// Path first, so the file sorts by path and a review diff reads as one line
// per changed file. Modes are part of the guarantee — a generated tree whose
// contents are identical on two machines but whose modes are not is not a
// deterministic tree, and no content comparison can see the difference.
//
// Paths are collected during the walk and read afterwards, so no filesystem
// operation happens inside the walk callback.
func treeManifest(t *testing.T, root, variantName string) string {
	t.Helper()

	type entry struct {
		rel  string
		dir  bool
		mode fs.FileMode
		full string
	}

	var entries []entry
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		entries = append(entries, entry{
			rel:  path.Clean(filepath.ToSlash(rel)),
			dir:  d.IsDir(),
			mode: info.Mode().Perm(),
			full: p,
		})
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.rel, b.rel) })

	var b strings.Builder
	fmt.Fprintf(&b, "# nise generated-tree golden manifest\n")
	fmt.Fprintf(&b, "# variant: %s\n", variantName)
	fmt.Fprintf(&b, "# sorted by path; regenerate with: %s=1 go test ./test/golden/...\n", updateEnv)
	fmt.Fprintf(&b, "#\n# <path> dir <mode>\n# <path> file <mode> <sha256>\n")

	for _, e := range entries {
		if e.dir {
			fmt.Fprintf(&b, "%s dir %04o\n", e.rel, e.mode)
			continue
		}
		data, err := os.ReadFile(e.full) // #nosec G304 -- a path from walking a directory this test created.
		if err != nil {
			t.Fatalf("reading %s: %v", e.full, err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s file %04o %s\n", e.rel, e.mode, hex.EncodeToString(sum[:]))
	}
	return b.String()
}

// assertManifestsMatch compares two manifests line by line and reports the
// first differing line, which is the file to look at.
//
// On Windows the mode column is dropped from both sides before comparing.
// Go maps Windows file permissions loosely — it honors only the write bit and
// synthesizes 0666/0777 for everything else — so an exact-mode assertion
// there would be asserting Go's mapping table rather than anything the
// generator controls. Paths and content hashes are still compared in full.
func assertManifestsMatch(t *testing.T, manifestPath, got, want string) {
	t.Helper()

	gotLines, wantLines := manifestLines(got), manifestLines(want)
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("%s is stale at line %d:\n  generated: %s\n  committed: %s\nRun `%s=1 go test ./test/golden/...` and review the diff if the change was intended.",
				manifestPath, i+1, gotLines[i], wantLines[i], updateEnv)
		}
	}
	switch {
	case len(gotLines) > len(wantLines):
		t.Fatalf("%s is missing %d entr(ies), starting with: %s\nRun `%s=1 go test ./test/golden/...`.",
			manifestPath, len(gotLines)-len(wantLines), gotLines[len(wantLines)], updateEnv)
	case len(wantLines) > len(gotLines):
		t.Fatalf("%s lists %d entr(ies) the generator no longer produces, starting with: %s\nRun `%s=1 go test ./test/golden/...`.",
			manifestPath, len(wantLines)-len(gotLines), wantLines[len(gotLines)], updateEnv)
	}
}

// manifestLines splits a manifest into comparable lines, dropping comments
// and — on Windows only — the mode column.
func manifestLines(manifest string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(manifest, "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if runtime.GOOS == "windows" {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				line = strings.Join(append(fields[:2:2], fields[3:]...), " ")
			}
		}
		out = append(out, line)
	}
	return out
}
