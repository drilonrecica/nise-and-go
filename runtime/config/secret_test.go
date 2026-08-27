package config

import (
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const wantValue = "super-secret-value"

// TestSecret_Unprintable proves that a Secret cannot be made to print its
// underlying value through any formatting or serialization path Go offers.
func TestSecret_Unprintable(t *testing.T) {
	s := NewSecret(wantValue)

	assertRedacted := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.Contains(got, wantValue) {
			t.Fatalf("%s leaked the secret value: %q", label, got)
		}
		if got != redacted {
			t.Fatalf("%s = %q, want %q", label, got, redacted)
		}
	}

	t.Run("Sprintf %v", func(t *testing.T) {
		assertRedacted(t, "%v", fmt.Sprintf("%v", s))
	})
	t.Run("Sprintf %s", func(t *testing.T) {
		// s is passed through an `any` so this genuinely exercises fmt's "%s"
		// verb handling at runtime (staticcheck cannot statically prove the
		// argument is a Stringer through the interface, which is exactly
		// what a real call site passing a Secret as an `any` looks like — so
		// this is not a workaround, it is the realistic shape of the check).
		var v any = s
		assertRedacted(t, "%s", fmt.Sprintf("%s", v))
	})
	t.Run("Sprintf %q", func(t *testing.T) {
		got := fmt.Sprintf("%q", s)
		if strings.Contains(got, wantValue) {
			t.Fatalf("%%q leaked the secret value: %q", got)
		}
		want := fmt.Sprintf("%q", redacted)
		if got != want {
			t.Fatalf("%%q = %q, want %q", got, want)
		}
	})
	t.Run("Sprintf %#v", func(t *testing.T) {
		got := fmt.Sprintf("%#v", s)
		if strings.Contains(got, wantValue) {
			t.Fatalf("%%#v leaked the secret value: %q", got)
		}
	})
	t.Run("combined verbs in one Sprintf", func(t *testing.T) {
		got := fmt.Sprintf("%v %s %#v %q", s, s, s, s)
		if strings.Contains(got, wantValue) {
			t.Fatalf("combined verbs leaked the secret value: %q", got)
		}
	})
	t.Run("String method", func(t *testing.T) {
		assertRedacted(t, "String()", s.String())
	})
	t.Run("GoString method", func(t *testing.T) {
		assertRedacted(t, "GoString()", s.GoString())
	})
	t.Run("json.Marshal", func(t *testing.T) {
		var _ json.Marshaler = s // compile-time contract check
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if strings.Contains(string(b), wantValue) {
			t.Fatalf("json.Marshal leaked the secret value: %s", b)
		}
		var got string
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if got != redacted {
			t.Fatalf("json round-trip = %q, want %q", got, redacted)
		}
	})
	t.Run("json.Marshal inside a struct", func(t *testing.T) {
		type wrapper struct {
			Password Secret `json:"password"`
		}
		b, err := json.Marshal(wrapper{Password: s})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if strings.Contains(string(b), wantValue) {
			t.Fatalf("struct json.Marshal leaked the secret value: %s", b)
		}
	})
	t.Run("MarshalText", func(t *testing.T) {
		var _ encoding.TextMarshaler = s // compile-time contract check
		b, err := s.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText: %v", err)
		}
		if strings.Contains(string(b), wantValue) {
			t.Fatalf("MarshalText leaked the secret value: %s", b)
		}
		if string(b) != redacted {
			t.Fatalf("MarshalText = %q, want %q", b, redacted)
		}
	})
	t.Run("slog.LogValuer", func(t *testing.T) {
		var _ slog.LogValuer = s // compile-time contract check
		buf := &strings.Builder{}
		logger := slog.New(slog.NewJSONHandler(buf, nil))
		logger.Info("startup", "db_password", s)
		out := buf.String()
		if strings.Contains(out, wantValue) {
			t.Fatalf("slog record leaked the secret value: %s", out)
		}
		if !strings.Contains(out, redacted) {
			t.Fatalf("slog record missing redaction placeholder: %s", out)
		}
	})
	t.Run("Reveal returns the real value", func(t *testing.T) {
		if got := s.Reveal(); got != wantValue {
			t.Fatalf("Reveal() = %q, want %q", got, wantValue)
		}
	})
}

func TestSecret_IsSet(t *testing.T) {
	if (Secret{}).IsSet() {
		t.Fatal("zero-value Secret reports IsSet() = true")
	}
	if !NewSecret("x").IsSet() {
		t.Fatal("non-empty Secret reports IsSet() = false")
	}
	if NewSecret("").IsSet() {
		t.Fatal("empty Secret reports IsSet() = true")
	}
}

func writeSecretFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestReadSecretFile_StripsTrailingNewline(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{"no newline", "hunter2", "hunter2"},
		{"lf", "hunter2\n", "hunter2"},
		{"crlf", "hunter2\r\n", "hunter2"},
		{"only interior newlines preserved", "line1\nline2", "line1\nline2"},
		{"interior newline plus trailing lf", "line1\nline2\n", "line1\nline2"},
		{"only one trailing lf stripped", "hunter2\n\n", "hunter2\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSecretFile(t, tt.contents)
			got, warning, err := readSecretFile(path)
			if err != nil {
				t.Fatalf("readSecretFile: %v", err)
			}
			if warning != "" {
				t.Fatalf("unexpected warning: %s", warning)
			}
			if got != tt.want {
				t.Fatalf("readSecretFile(%q) = %q, want %q", tt.contents, got, tt.want)
			}
		})
	}
}

func TestReadSecretFile_MissingFile(t *testing.T) {
	_, _, err := readSecretFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestReadSecretFile_Directory(t *testing.T) {
	_, _, err := readSecretFile(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a directory")
	}
}

func TestReadSecretFile_Empty(t *testing.T) {
	path := writeSecretFile(t, "")
	_, _, err := readSecretFile(path)
	if err == nil {
		t.Fatal("expected an error for an empty file")
	}
}

func TestReadSecretFile_NewlineOnlyIsEmpty(t *testing.T) {
	path := writeSecretFile(t, "\n")
	_, _, err := readSecretFile(path)
	if err == nil {
		t.Fatal("expected an error for a file containing only a newline")
	}
}

func TestReadSecretFile_TooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.Truncate(MaxSecretFileSize + 1); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, _, err = readSecretFile(path)
	if err == nil {
		t.Fatal("expected an error for a file larger than MaxSecretFileSize")
	}
}

func TestReadSecretFile_PermissionsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("hunter2"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	value, warning, err := readSecretFile(path)
	if err != nil {
		t.Fatalf("readSecretFile: %v", err)
	}
	if value != "hunter2" {
		t.Fatalf("value = %q, want %q", value, "hunter2")
	}
	if warning == "" {
		t.Fatal("expected a permissions warning for a group/world readable file")
	}
	if strings.Contains(warning, "hunter2") {
		t.Fatalf("warning leaked the secret value: %s", warning)
	}
}

func TestReadSecretFile_OwnerOnlyPermissionsNoWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on windows")
	}
	path := writeSecretFile(t, "hunter2") // written with 0o600 by the helper
	_, warning, err := readSecretFile(path)
	if err != nil {
		t.Fatalf("readSecretFile: %v", err)
	}
	if warning != "" {
		t.Fatalf("unexpected warning for an owner-only file: %s", warning)
	}
}

func TestCheckSecretFilePermissions_SkippedOnWindows(t *testing.T) {
	// checkSecretFilePermissions must return false unconditionally on
	// Windows; this test only asserts the runtime.GOOS branch exists and is
	// reachable, since we cannot change GOOS at test time.
	if runtime.GOOS != "windows" {
		t.Skip("only meaningful on windows")
	}
	path := writeSecretFile(t, "hunter2")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if checkSecretFilePermissions(info) {
		t.Fatal("checkSecretFilePermissions() = true on windows, want false")
	}
}
