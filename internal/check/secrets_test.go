package check_test

import (
	"context"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
)

// trackedLister returns a TrackedLister that reports exactly the given files
// as git-tracked, so the tracked-file path is exercised without needing a
// real repository — and so a test can prove that an untracked file is not
// scanned.
func trackedLister(files ...string) check.TrackedLister {
	return func(_ context.Context, _ string) ([]string, bool, error) {
		return files, true, nil
	}
}

func withGit(git check.TrackedLister) func(*check.Options) {
	return func(o *check.Options) { o.Git = git }
}

// TestGeneratedTreeCarriesNoSecretMaterial is the first-context case: the
// scan must be quiet on the tree `nise new` writes, including .env.example,
// which is full of secret-named variables with placeholder values on
// purpose. A check that fires there is a check nobody keeps enabled.
func TestGeneratedTreeCarriesNoSecretMaterial(t *testing.T) {
	t.Parallel()

	c := assertStatus(t, run(t, newProject(t)), check.CheckSecrets, check.StatusOK)
	if !strings.Contains(c.Detail, "not a git repository") {
		t.Errorf("detail = %q, want it to say the whole tree was scanned in the absence of a repository", c.Detail)
	}
}

// TestSecretMaterialFindings covers each rule, and each rule's tolerance.
// The tolerances matter as much as the detections: every false positive here
// is a correct project failing CI.
func TestSecretMaterialFindings(t *testing.T) {
	t.Parallel()

	// #nosec G101 -- deliberately not a key: fourteen characters of base64
	// that decode to nothing, wrapped in the armor the rule under test
	// looks for. A test for a private-key detector has to contain the
	// string the detector matches.
	const privateKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----\n"

	for _, tc := range []struct {
		name    string
		path    string
		content string
		want    check.Status
	}{
		{
			name:    "a committed environment file",
			path:    ".env",
			content: "APP_ENV=production\n",
			want:    check.StatusFail,
		},
		{
			name:    "a committed per-environment file",
			path:    ".env.production",
			content: "APP_ENV=production\n",
			want:    check.StatusFail,
		},
		{
			name:    "the example file is exactly what may be committed",
			path:    ".env.example",
			content: "OPERATOR_TOKEN=\nDATABASE_PASSWORD=changeme\nAPI_KEY=<your-key-here>\n",
			want:    check.StatusOK,
		},
		{
			name:    "a private key block in any file",
			path:    "deploy/notes.md",
			content: "Here is the key we use:\n" + privateKey,
			want:    check.StatusFail,
		},
		{
			name:    "a file named like a private key",
			path:    "deploy/server.key",
			content: "not actually a key, but named like one\n",
			want:    check.StatusFail,
		},
		{
			name:    "a real value assigned to a secret-named variable",
			path:    "config/staging.env",
			content: "OPERATOR_TOKEN=Yb3kQ9wLmPz2R7tXvA4s\n",
			want:    check.StatusFail,
		},
		{
			name:    "a short stub value is not material",
			path:    "config/staging.env",
			content: "OPERATOR_TOKEN=dev\n",
			want:    check.StatusOK,
		},
		{
			name:    "a placeholder value is not material",
			path:    "config/staging.env",
			content: "OPERATOR_TOKEN=${OPERATOR_TOKEN}\nDB_PASSWORD=your-password-here\n",
			want:    check.StatusOK,
		},
		{
			name:    "a secret-shaped identifier in source is not an assignment",
			path:    "internal/platform/httpapi/handler.go",
			content: "package httpapi\n\nfunc h() { token := cfg.OperatorToken; _ = token }\n",
			want:    check.StatusOK,
		},
		{
			name:    "a connection string with an inline password",
			path:    "deploy/compose.yaml",
			content: "    DATABASE_URL: postgres://svc:Kd82hsQ0zLpW@db.internal:5432/app\n",
			want:    check.StatusFail,
		},
		{
			name:    "a throwaway local credential is not a leak",
			path:    "deploy/compose.yaml",
			content: "    DATABASE_URL: postgres://app:app@localhost:5432/app\n",
			want:    check.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := newProject(t)
			writeFile(t, root, tc.path, tc.content)

			c := assertStatus(t, run(t, root, withGit(trackedLister(tc.path))), check.CheckSecrets, tc.want)
			if tc.want == check.StatusFail && !containsPath(c.Paths, tc.path) {
				t.Errorf("paths = %v, want it to name %q", c.Paths, tc.path)
			}
		})
	}
}

// TestOnlyTrackedFilesAreScannedWhenGitAnswers is the whole reason git is
// consulted at all: a local .env that .gitignore excludes is not committed
// secret material, and reporting it would train people to ignore the check.
func TestOnlyTrackedFilesAreScannedWhenGitAnswers(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, ".env", "OPERATOR_TOKEN=Yb3kQ9wLmPz2R7tXvA4s\n")

	// git reports only the recipe as tracked; the .env is ignored.
	assertStatus(t, run(t, root, withGit(trackedLister("nise.json"))), check.CheckSecrets, check.StatusOK)

	// The same file, tracked, is a finding.
	assertStatus(t, run(t, root, withGit(trackedLister(".env"))), check.CheckSecrets, check.StatusFail)
}

// TestSecretFindingRemedyNamesRotation covers the one thing the remedy must
// say. Deleting a committed secret in a new commit leaves it readable in
// every earlier one, so "remove it" alone is advice that leaves the reader
// exposed while believing they are fixed.
func TestSecretFindingRemedyNamesRotation(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, ".env.production", "OPERATOR_TOKEN=Yb3kQ9wLmPz2R7tXvA4s\n")

	c := assertStatus(t, run(t, root, withGit(trackedLister(".env.production"))), check.CheckSecrets, check.StatusFail)
	for _, want := range []string{"rotate", "history"} {
		if !strings.Contains(strings.ToLower(c.Remedy), want) {
			t.Errorf("remedy = %q, want it to mention %q", c.Remedy, want)
		}
	}
}

// TestBinaryFilesAreNotScannedForText guards against the scan producing
// unreadable findings from a binary file that happens to contain a matching
// byte sequence.
func TestBinaryFilesAreNotScannedForText(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	writeFile(t, root, "frontend/static/blob.bin", "\x00\x01-----BEGIN RSA PRIVATE KEY-----\x00")

	assertStatus(t, run(t, root, withGit(trackedLister("frontend/static/blob.bin"))), check.CheckSecrets, check.StatusOK)
}
