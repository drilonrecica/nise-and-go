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
			// The positive control for the case above. Without it, that
			// non-detection could pass because the NAME never matched
			// rather than because the placeholder tolerance worked — which
			// is exactly how a bare-name blind spot survived review once
			// already. Same file, same variable names, real values.
			name:    "the same names in the same file with real values are found",
			path:    ".env.example",
			content: "OPERATOR_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a\nDATABASE_PASSWORD=Xb9-qt2LmZ0pRvW4\nAPI_KEY=AIzaSyA1B2C3D4E5F6G7H8I9J0KlMnOpQrStUv\n",
			want:    check.StatusFail,
		},
		// #nosec G101 -- not a credential: a fixture shaped like a GitHub
		// personal access token, so the rule under test has something to
		// match. gosec flagging it is the detector this test asserts,
		// working.
		{
			// The bare spellings. docs/commands/check.md documents the rule
			// as *TOKEN* / *SECRET*, where the star means "any affix,
			// including none"; an earlier pattern required at least one
			// character before the keyword, so a real GitHub token
			// committed as "TOKEN=ghp_..." scanned clean.
			name:    "a bare TOKEN with a real value",
			path:    ".env.example",
			content: "TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a\n",
			want:    check.StatusFail,
		},
		{
			name:    "a bare SECRET with a real value",
			path:    ".env.example",
			content: "SECRET=s3cr3t-Yb3kQ9wLmPz2R7tXvA4s\n",
			want:    check.StatusFail,
		},
		{
			name:    "a bare API_KEY with a real value",
			path:    ".env.example",
			content: "API_KEY=AIzaSyA1B2C3D4E5F6G7H8I9J0KlMnOpQrStUv\n",
			want:    check.StatusFail,
		},
		{
			name:    "a bare PASSWORD with a real value",
			path:    "config/staging.env",
			content: "PASSWORD=Xb9-qt2LmZ0pRvW4\n",
			want:    check.StatusFail,
		},
		{
			// Encrypted environment files are correct practice, not a
			// mistake, and there is no suppression mechanism in this slice:
			// failing them would leave such a project unable to make
			// `nise check` green at all.
			name:    "an encrypted environment file may be committed",
			path:    ".env.enc",
			content: "OPERATOR_TOKEN=ENC[AES256_GCM,data:9xKq2mVvQ0pR7tXwA4sYb3kQ,type:str]\n",
			want:    check.StatusOK,
		},
		{
			name:    "a sops-encrypted per-environment file may be committed",
			path:    ".env.production.sops",
			content: "DATABASE_PASSWORD=ENC[AES256_GCM,data:Kd82hsQ0zLpWmVvQ0pR7,type:str]\n",
			want:    check.StatusOK,
		},
		{
			name:    "an age-encrypted environment file may be committed",
			path:    ".env.age",
			content: "-----BEGIN AGE ENCRYPTED FILE-----\nYWdlLWVuY3J5cHRpb24ub3Jn\n-----END AGE ENCRYPTED FILE-----\n",
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
			// PEM is an armor encoding, not a key format. A .pem holding a
			// public certificate or a CA bundle is a correct thing to
			// commit, and rule 3 already finds a real private key inside
			// any text file by its armor header.
			name:    "a .pem holding only a public certificate",
			path:    "deploy/tls/ca.pem",
			content: "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAK\n-----END CERTIFICATE-----\n",
			want:    check.StatusOK,
		},
		{
			name:    "a .pem that really does hold a private key",
			path:    "deploy/tls/server.pem",
			content: privateKey,
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
		// #nosec G101 -- not a credential: the literal word "password" in a
		// documentation example, which is precisely the non-detection this
		// case exists to pin.
		{
			// Documentation that shows the shape of a connection string is
			// the single most common place a DSN appears in a repository,
			// and the placeholder tolerance is what keeps prose quiet.
			name:    "a documented example DSN in prose",
			path:    "db/README.md",
			content: "Set `DATABASE_URL`, for example:\n\n    postgres://user:password@localhost:5432/appdb\n",
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
