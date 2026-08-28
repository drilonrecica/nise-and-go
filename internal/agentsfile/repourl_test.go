package agentsfile

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// blobLinkRE captures the branch segment of any link into this repository's
// file browser, for example the "master" in
// https://github.com/drilonrecica/nise-and-go/blob/master/docs/….
var blobLinkRE = regexp.MustCompile(`github\.com/drilonrecica/nise-and-go/blob/([A-Za-z0-9._/-]+)`)

// TestRepositoryBlobLinksUseOneBranch is the structural guard behind
// niseRepoBlobURL. Two independent authors once spelled this link two ways —
// generated AGENTS.md said blob/main while runtime/secure said blob/master —
// and only one of them was right, so every generated project shipped five
// dead links. The golden fixtures could not catch it: they pinned the wrong
// value, so they agreed with the defect.
//
// This test does what a fixture cannot: it reads the whole worktree and
// asserts that every repository blob link anywhere — Go source, templates,
// documentation, and the golden fixtures themselves — names the branch
// niseRepoDefaultBranch declares. Renaming the default branch is therefore
// one edit in agentsmd.go plus a fixture regeneration, and a second spelling
// cannot be introduced silently.
func TestRepositoryBlobLinksUseOneBranch(t *testing.T) {
	dir := repoRoot(t)

	// os.Root confines every read below to the repository worktree, so a
	// symlink inside it cannot make this test read a file outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("opening %s: %v", dir, err)
	}
	defer func() { _ = root.Close() }()

	var checked int
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "privateDocs":
				return fs.SkipDir
			}
			return nil
		}
		if !textFileExtension(path.Ext(d.Name())) {
			return nil
		}
		data, readErr := fs.ReadFile(root.FS(), name)
		if readErr != nil {
			return readErr
		}
		for _, m := range blobLinkRE.FindAllStringSubmatch(string(data), -1) {
			checked++
			branch, _, _ := strings.Cut(m[1], "/")
			if branch != niseRepoDefaultBranch {
				t.Errorf("%s links to blob/%s; the repository's default branch is %q. "+
					"Build repository links from niseRepoBlobURL (internal/agentsfile/agentsmd.go) "+
					"so there is exactly one branch name in the tree.", name, branch, niseRepoDefaultBranch)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", dir, walkErr)
	}
	if checked == 0 {
		t.Fatalf("found no repository blob links under %s; the guard is not actually checking anything", dir)
	}
}

// textFileExtension reports whether a file with this extension is worth
// scanning for a link. Restricting by extension keeps the walk off binaries
// and keeps the test fast; every place a link can plausibly be written —
// Go source, templates, Markdown, JSON, YAML — is covered.
func textFileExtension(ext string) bool {
	switch ext {
	case ".go", ".tmpl", ".md", ".json", ".yml", ".yaml", ".txt", ".html", ".ts", ".svelte", ".css":
		return true
	default:
		return false
	}
}

// repoRoot walks up from the test's working directory to the module root,
// identified by the go.mod that declares this module.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}
