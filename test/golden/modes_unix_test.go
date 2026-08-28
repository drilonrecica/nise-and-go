//go:build !windows

package golden_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// This file exists for one reason: syscall.Umask is not defined on Windows.
//
// A runtime `if runtime.GOOS == "windows" { t.Skip(...) }` guard does not
// help, because the reference still has to resolve when the test binary is
// compiled — and a package's test binary is all-or-nothing, so an
// unresolvable symbol in one test fails this whole suite on that platform,
// not merely the test that needs it. The build tag is what keeps the
// reference off a Windows compile entirely. That exact mistake has broken
// CI's Windows leg in this repository once already.
//
// There is deliberately no modes_windows_test.go counterpart. Windows has no
// umask, and its file permissions are not the Unix mode bits: Go honors only
// the write bit and synthesizes 0666/0777 for everything else, so an
// exact-mode assertion there would assert Go's mapping table rather than
// anything the generator controls. assertManifestsMatch already drops the
// mode column on Windows for the same reason, and everything else this suite
// pins — paths and content hashes — is compared in full on every platform.

// TestGoldenModesHoldUnderAHostileUmask is the regression test for the
// committed manifest's mode column meaning anything at all.
//
// os.WriteFile and os.MkdirAll mask their mode argument with the process
// umask, so a tree generated under `umask 077` would come out 0600/0700
// while the committed manifest says 0644/0755 — two machines producing trees
// whose contents are identical and whose modes are not, which no content
// comparison can see. internal/generator calls os.Chmod explicitly to defeat
// that; this test is what proves the call is still there, by asserting the
// committed manifest under a maximally restrictive umask.
//
// The umask is process-wide state, so this test does not call t.Parallel and
// restores the previous value before returning.
func TestGoldenModesHoldUnderAHostileUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	for _, v := range variants() {
		t.Run(v.Name, func(t *testing.T) {
			got := treeManifest(t, generate(t, v), v.Name)

			manifestPath := filepath.Join("testdata", v.Name, "manifest.txt")
			want, err := os.ReadFile(manifestPath) // #nosec G304 -- a path built from this test's own literals.
			if err != nil {
				t.Fatalf("reading %s: %v", manifestPath, err)
			}
			assertManifestsMatch(t, manifestPath, got, string(want))
		})
	}
}
