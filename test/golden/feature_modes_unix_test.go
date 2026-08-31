//go:build !windows

package golden_test

import (
	"syscall"
	"testing"
)

// A generated feature's file modes are fixed literals, not whatever the
// process umask leaves behind.
//
// This is the determinism failure no content comparison can see: two machines
// producing trees whose bytes are identical and whose modes are not.
// feature.Write chmods explicitly to defeat the umask; this is what proves the
// call is still there.
//
// It lives in a build-tagged file because syscall.Umask does not exist on
// Windows, and an unresolvable symbol in one test fails the whole package's
// test binary on that platform rather than only the test that needs it — a
// mistake that has broken this repository's Windows leg once already.
func TestFeatureModesHoldUnderAHostileUmask(t *testing.T) {
	// The umask is process-wide, so this does not run in parallel.
	v := featureVariants()[1]
	baseline := writeFeature(t, v.Options, v.Name)

	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	if got := writeFeature(t, v.Options, v.Name); got != baseline {
		t.Errorf("a hostile umask changed the generated tree:\n--- baseline ---\n%s\n--- umask 077 ---\n%s", baseline, got)
	}
}
