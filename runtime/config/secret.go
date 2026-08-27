package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
)

// redacted is returned by every formatting and serialization path on Secret
// in place of the real value.
const redacted = "[REDACTED]"

// MaxSecretFileSize is the largest file [Loader.Secret] will read for a
// `_FILE` secret indirection. A file larger than this is rejected so that a
// misconfigured path (for example one that points at a large unrelated file)
// cannot exhaust memory.
const MaxSecretFileSize = 1 << 20 // 1 MiB

// Secret holds a sensitive configuration value: a password, API key,
// connection-string credential, or similar. Its zero value is an unset
// secret.
//
// A Secret is unprintable through every formatting and serialization path Go
// offers: [Secret.String], [Secret.GoString], [Secret.MarshalJSON],
// [Secret.MarshalText], and [Secret.LogValue] all return a fixed redaction
// placeholder regardless of the underlying value. This holds for every fmt
// verb ("%v", "%s", "%q", "%#v") because fmt consults [fmt.Stringer] and
// [fmt.GoStringer] before falling back to reflection. The only way to obtain
// the real value is the explicit [Secret.Reveal] method.
type Secret struct {
	value string
}

// NewSecret wraps value as a Secret. Use it to bring a sensitive value
// obtained outside [Loader] (for example one derived at runtime) under the
// same redaction guarantee as a loaded configuration secret.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal returns the underlying value. It is the single explicit accessor
// for a Secret's contents; every other method on Secret returns a redaction
// placeholder. Call it only where the real value is genuinely needed (for
// example, to open a database connection), never to log or display it.
func (s Secret) Reveal() string {
	return s.value
}

// IsSet reports whether the secret has a non-empty value.
func (s Secret) IsSet() bool {
	return s.value != ""
}

// String implements [fmt.Stringer]. It always returns the redaction
// placeholder, never the underlying value.
func (s Secret) String() string {
	return redacted
}

// GoString implements [fmt.GoStringer], the interface fmt consults for the
// "%#v" verb. It always returns the redaction placeholder.
func (s Secret) GoString() string {
	return redacted
}

// MarshalJSON implements [encoding/json.Marshaler]. It always encodes the
// redaction placeholder, never the underlying value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(redacted)
}

// MarshalText implements [encoding.TextMarshaler]. It always returns the
// redaction placeholder, never the underlying value.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}

// LogValue implements [log/slog.LogValuer] so that passing a Secret directly
// to a slog call redacts it automatically instead of relying on every call
// site to remember to do so.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// readSecretFile reads the `_FILE` indirection target for a secret. It
// strips exactly one trailing newline ("\n" or "\r\n") and fails if the file
// is missing, unreadable, a directory, larger than [MaxSecretFileSize], or
// empty (before or after stripping the newline). It returns a non-empty
// warning string, instead of an error, when the file is readable by group or
// other on a Unix platform; the check is skipped on Windows.
func readSecretFile(path string) (value string, warning string, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			return "", "", fmt.Errorf("file %q does not exist", path)
		}
		return "", "", fmt.Errorf("file %q could not be opened: %w", path, openErr)
	}
	defer f.Close()

	info, statErr := f.Stat()
	if statErr != nil {
		return "", "", fmt.Errorf("file %q could not be read: %w", path, statErr)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("file %q is a directory, not a file", path)
	}
	if info.Size() == 0 {
		return "", "", fmt.Errorf("file %q is empty", path)
	}
	if info.Size() > MaxSecretFileSize {
		return "", "", fmt.Errorf("file %q is larger than the %d byte limit", path, MaxSecretFileSize)
	}

	if checkSecretFilePermissions(info) {
		warning = fmt.Sprintf("file %q is readable by group or other (mode %s); restrict it to the owner only", path, info.Mode().Perm())
	}

	// Read one byte more than the limit already checked via Stat so a file
	// that grows between Stat and Read is still rejected rather than
	// silently truncated.
	data, readErr := io.ReadAll(io.LimitReader(f, MaxSecretFileSize+1))
	if readErr != nil {
		return "", "", fmt.Errorf("file %q could not be read: %w", path, readErr)
	}
	if len(data) > MaxSecretFileSize {
		return "", "", fmt.Errorf("file %q is larger than the %d byte limit", path, MaxSecretFileSize)
	}

	raw := string(data)
	trimmed := strings.TrimSuffix(raw, "\r\n")
	if trimmed == raw {
		trimmed = strings.TrimSuffix(raw, "\n")
	}
	raw = trimmed

	if raw == "" {
		return "", warning, fmt.Errorf("file %q contains an empty value", path)
	}
	return raw, warning, nil
}

// checkSecretFilePermissions reports whether info's permissions are readable
// by group or other. It always reports false on Windows, where the POSIX
// permission bits this check relies on do not apply.
func checkSecretFilePermissions(info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return info.Mode().Perm()&0o044 != 0
}
