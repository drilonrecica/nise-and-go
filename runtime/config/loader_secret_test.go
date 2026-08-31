package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoader_Secret_FromEnvironment(t *testing.T) {
	t.Setenv("DB_PASSWORD", "hunter2")
	l := NewLoader()
	s := l.Secret("DB_PASSWORD")
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Reveal() != "hunter2" {
		t.Fatalf("Reveal() = %q, want %q", s.Reveal(), "hunter2")
	}
}

func TestLoader_Secret_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db_password")
	if err := os.WriteFile(path, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DB_PASSWORD_FILE", path)

	l := NewLoader()
	s := l.Secret("DB_PASSWORD")
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Reveal() != "hunter2" {
		t.Fatalf("Reveal() = %q, want %q", s.Reveal(), "hunter2")
	}
}

// TestLoader_Secret_BothSetIsAnError proves that setting both FOO and
// FOO_FILE is always an error, never a silent preference for one source.
func TestLoader_Secret_BothSetIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db_password")
	if err := os.WriteFile(path, []byte("from-file"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DB_PASSWORD", "from-env")
	t.Setenv("DB_PASSWORD_FILE", path)

	l := NewLoader()
	s := l.Secret("DB_PASSWORD")

	err := l.Err()
	if err == nil {
		t.Fatal("expected an error when both DB_PASSWORD and DB_PASSWORD_FILE are set")
	}
	if !strings.Contains(err.Error(), "DB_PASSWORD") {
		t.Fatalf("error %q does not name the variable", err)
	}
	if strings.Contains(err.Error(), "from-env") || strings.Contains(err.Error(), "from-file") {
		t.Fatalf("error leaked a secret value: %v", err)
	}
	if s.IsSet() {
		t.Fatal("expected an unset Secret when loading fails")
	}
}

func TestLoader_Secret_MissingIsAnError(t *testing.T) {
	l := NewLoader()
	l.Secret("MISSING_SECRET")
	if err := l.Err(); err == nil {
		t.Fatal("expected an error for a missing required secret")
	}
}

func TestLoader_Secret_Default(t *testing.T) {
	l := NewLoader()
	s := l.Secret("MISSING_SECRET_WITH_DEFAULT", Default("dev-only"))
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Reveal() != "dev-only" {
		t.Fatalf("Reveal() = %q, want %q", s.Reveal(), "dev-only")
	}
}

func TestLoader_Secret_FileErrorNamesVariableNotValue(t *testing.T) {
	t.Setenv("DB_PASSWORD_FILE", filepath.Join(t.TempDir(), "does-not-exist"))
	l := NewLoader()
	l.Secret("DB_PASSWORD")

	err := l.Err()
	if err == nil {
		t.Fatal("expected an error for a missing secret file")
	}
	if !strings.Contains(err.Error(), "DB_PASSWORD") {
		t.Fatalf("error %q does not name the variable", err)
	}
}

// TestLoader_Secret_PermissionWarningIsCollected proves the warning
// readSecretFile produces for a group- or world-readable secret file
// survives the trip through Loader and is reported to the caller, rather
// than being produced and then dropped.
//
// It skips on Windows for the same reason
// TestReadSecretFile_PermissionsWarning does: checkSecretFilePermissions
// deliberately returns false there, because the POSIX bits it reads do not
// describe who can actually open the file. Asserting a warning appears would
// be asserting against the documented behaviour.
func TestLoader_Secret_PermissionWarningIsCollected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "db_password")
	if err := os.WriteFile(path, []byte("hunter2"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DB_PASSWORD_FILE", path)

	l := NewLoader()
	l.Secret("DB_PASSWORD")
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warnings := l.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "DB_PASSWORD") {
		t.Fatalf("warning %q does not name the variable", warnings[0])
	}
	if strings.Contains(warnings[0], "hunter2") {
		t.Fatalf("warning leaked the secret value: %s", warnings[0])
	}
}

// TestLoader_Err_CollectsSecretViolationsToo proves that a missing/invalid
// Secret field joins the same combined error as ordinary fields, rather
// than failing through a separate, easily-forgotten channel.
func TestLoader_Err_CollectsSecretViolationsToo(t *testing.T) {
	l := NewLoader()
	l.String("MISSING_STRING")
	l.Secret("MISSING_SECRET")

	err := l.Err()
	if err == nil {
		t.Fatal("expected a combined error")
	}
	for _, name := range []string{"MISSING_STRING", "MISSING_SECRET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("combined error missing violation for %s: %v", name, err)
		}
	}
}
