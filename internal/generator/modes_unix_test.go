//go:build !windows

package generator_test

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// This file exists for one reason: syscall.Umask is not defined on Windows.
//
// A runtime `if runtime.GOOS == "windows" { t.Skip(...) }` guard does not
// help, because the reference still has to resolve when the test binary is
// compiled — and a package's test binary is all-or-nothing, so an
// unresolvable symbol in one test fails the whole internal/generator suite
// on that platform, not merely the test that needs it. The build tag is
// what keeps the reference off a Windows compile entirely.
//
// There is deliberately no modes_windows_test.go counterpart. Windows has
// no umask, and its file permissions are not the Unix mode bits: Go maps
// them loosely, honoring only the write bit (a file is read-only or it is
// not) and reporting synthesized 0666/0777 values for everything else. An
// exact-mode assertion there would be asserting Go's mapping table rather
// than anything this generator controls. What does matter on Windows —
// that generation succeeds and produces the right paths and bytes — is
// already covered by the golden and determinism tests, which build and run
// on every platform. This file is not missing a sibling.

// TestFileModesAreIndependentOfUmask is the regression test for the umask
// dependency itself: it sets a maximally restrictive umask for the
// duration of the generation and requires the modes to come out unchanged.
//
// It cannot run in parallel with anything, because the umask is
// process-wide state, so it does not call t.Parallel and restores the
// previous value before returning.
func TestFileModesAreIndependentOfUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	root := filepath.Join(t.TempDir(), "myapp")
	if _, err := generator.Write(root, defaultOptions()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertExactModes(t, root)
}
