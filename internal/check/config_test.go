package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/check"
)

// envFile writes an environment file outside the project tree — where a real
// production env file lives — and returns its path.
func envFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "production.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the env file: %v", err)
	}
	return path
}

// withEnvFile points the run at an env file.
func withEnvFile(path string) func(*check.Options) {
	return func(o *check.Options) { o.EnvFile = path }
}

// TestConfigCheckSkipsWithoutAnEnvFile pins the deliberate absence of a
// default. Guessing at ".env" would validate a developer's local file as
// though it were the production one and report on the wrong thing.
func TestConfigCheckSkipsWithoutAnEnvFile(t *testing.T) {
	t.Parallel()

	c := assertStatus(t, run(t, newProject(t)), check.CheckConfig, check.StatusSkipped)
	if !strings.Contains(c.Detail, "--env-file") {
		t.Errorf("detail = %q, want it to name the flag that would enable the check", c.Detail)
	}
}

// TestGeneratedEnvExamplePasses is the first-context case for this check
// too: the file `nise new` ships must not be reported as broken.
func TestGeneratedEnvExamplePasses(t *testing.T) {
	t.Parallel()

	root := newProject(t)
	assertStatus(t, run(t, root, withEnvFile(filepath.Join(root, ".env.example"))), check.CheckConfig, check.StatusOK)
}

// TestConfigInvariantViolations covers every rule the check enforces. Each
// one is a rule runtime/config or runtime/lifecycle applies before any
// application code runs, so no application can opt out of it by editing a
// file it owns.
func TestConfigInvariantViolations(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		content  string
		want     check.Status
		wantText string
	}{
		{
			name:     "an unrecognized APP_ENV",
			content:  "APP_ENV=staging\n",
			want:     check.StatusFail,
			wantText: "APP_ENV",
		},
		{
			name:     "an unrecognized APP_MODE",
			content:  "APP_ENV=production\nAPP_MODE=everything\n",
			want:     check.StatusFail,
			wantText: "APP_MODE",
		},
		{
			name:     "a secret supplied twice",
			content:  "APP_ENV=production\nOPERATOR_TOKEN=abc\nOPERATOR_TOKEN_FILE=/run/secrets/op\n",
			want:     check.StatusFail,
			wantText: "OPERATOR_TOKEN and OPERATOR_TOKEN_FILE are both set",
		},
		{
			name:     "a line that is not an assignment",
			content:  "APP_ENV=production\nthis is not an assignment\n",
			want:     check.StatusFail,
			wantText: "line 2",
		},
		{
			name:     "a production file that breaks no runtime rule",
			content:  "# a comment\n\nexport APP_ENV=production\nAPP_MODE=web\nPORT=8080\nOPERATOR_TOKEN_FILE=/run/secrets/op\n",
			want:     check.StatusOK,
			wantText: "production",
		},
		{
			name:     "quoted values and inline comments",
			content:  "APP_ENV=\"production\"\nAPP_MODE='worker'\nBIND_ADDR=10.0.0.1 # private\n",
			want:     check.StatusOK,
			wantText: "production",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := assertStatus(t, run(t, newProject(t), withEnvFile(envFile(t, tc.content))), check.CheckConfig, tc.want)
			if !strings.Contains(c.Detail, tc.wantText) {
				t.Errorf("detail = %q, want it to contain %q", c.Detail, tc.wantText)
			}
		})
	}
}

// TestConfigCheckDoesNotEnforceApplicationPolicy is the exclusion boundary,
// asserted rather than only documented. DEBUG, LOG_FORMAT, and the
// BIND_ADDR/ALLOW_PUBLIC_BIND pairing are enforced by
// internal/platform/config/config.go, which is application-owned (ADR 0003)
// and which the application may legitimately replace. Failing here would be
// enforcing starter conformity on a file whose ownership model says the
// application decides — and would fail a correct project.
//
// If a future change makes this test fail, the question to answer first is
// not "how do I make it pass" but "has the ownership model changed".
func TestConfigCheckDoesNotEnforceApplicationPolicy(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		"APP_ENV=production",
		"DEBUG=true",
		"LOG_FORMAT=text",
		"BIND_ADDR=0.0.0.0",
		"ALLOW_PUBLIC_BIND=false",
	}, "\n") + "\n"

	assertStatus(t, run(t, newProject(t), withEnvFile(envFile(t, content))), check.CheckConfig, check.StatusOK)
}

// TestUnreadableEnvFileFails covers the one thing about the flag itself that
// is a failure rather than a skip: the user named a file and it could not be
// read, so the check they asked for did not happen and they must be told.
func TestUnreadableEnvFileFails(t *testing.T) {
	t.Parallel()

	c := assertStatus(t, run(t, newProject(t), withEnvFile(filepath.Join(t.TempDir(), "absent.env"))), check.CheckConfig, check.StatusFail)
	if !strings.Contains(c.Remedy, "--env-file") {
		t.Errorf("remedy = %q, want it to name the flag", c.Remedy)
	}
}
