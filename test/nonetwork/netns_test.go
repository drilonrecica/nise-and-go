package nonetwork

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestDynamicNetworkNamespace is the stricter mechanism
// TestDynamicNoOutboundConnections' own doc comment says to consider: it
// re-runs every case in dynamicCases() inside a network namespace created
// by `unshare --user --net`, which has no interface but a loopback that is
// never brought up. There is no default route and nothing listening
// anywhere, so any connection attempt fails immediately at the kernel —
// including a raw net.Dial that would ignore HTTP_PROXY entirely and sail
// straight past TestDynamicNoOutboundConnections' recording listener.
//
// This needs no root: `unshare --user` creates a *user* namespace first
// (mapping the calling user to uid 0 inside it) and that new namespace's
// owner is then permitted to also unshare the network namespace — the
// standard unprivileged-userns path most Linux distributions ship enabled.
// Where it is not enabled (locked-down kernels, some container sandboxes,
// and some CI runners deliberately disable unprivileged user namespaces),
// this test skips with an explicit reason rather than failing the build:
// the property it adds is real but strictly additional to
// TestDynamicNoOutboundConnections, which runs unconditionally and already
// satisfies the task's required dynamic proof.
//
// What a pass here proves that the proxy-based test cannot: that none of
// these commands' behavior — exit code, stdout, or a network-shaped error
// message — depends on whether the network exists at all. What it still
// cannot prove: a command that behaves *identically* whether or not it
// could reach the network (e.g. one that fires a dial and explicitly
// ignores its result) would pass both tests silently. Nothing here reads
// packet captures; both proofs are behavioral.
func TestDynamicNetworkNamespace(t *testing.T) {
	requireUnprivilegedNetworkNamespace(t)

	for _, c := range dynamicCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			res := runNiseInIsolatedNetworkNamespace(t, dir, c.args...)

			if c.wantExit != nil && res.exitCode != *c.wantExit {
				t.Errorf("nise %s under an isolated network namespace: exit code = %d, want %d\nstdout=%q\nstderr=%q",
					strings.Join(c.args, " "), res.exitCode, *c.wantExit, res.stdout, res.stderr)
			}
			if c.extraChecks != nil {
				c.extraChecks(t, res)
			}
			// The interesting assertion: if any code path attempted a raw
			// dial that HTTP_PROXY cannot see, running with no network at
			// all turns that attempt into an immediate, distinctive error
			// ("network is unreachable" or similar). Seeing one here,
			// where TestDynamicNoOutboundConnections saw nothing, is
			// exactly the gap that test's doc comment describes.
			assertNoNetworkErrorMarkers(t, res)
		})
	}
}

// requireUnprivilegedNetworkNamespace skips the test when this sandbox
// cannot create an unprivileged user+network namespace, rather than
// failing the suite over an environment limitation this task's dynamic
// proof does not depend on.
func requireUnprivilegedNetworkNamespace(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skipf("nonetwork: network namespace isolation is Linux-only (unshare(1)); skipping on %s", runtime.GOOS)
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("nonetwork: unshare(1) not found on PATH; skipping the stricter network-namespace proof")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- fixed arguments, no user input.
	probe := exec.CommandContext(ctx, "unshare", "--user", "--net", "--map-root-user", "--", "true")
	var stderr bytes.Buffer
	probe.Stderr = &stderr
	if err := probe.Run(); err != nil {
		t.Skipf("nonetwork: this sandbox cannot create an unprivileged user+network namespace (%v: %s); "+
			"skipping the stricter proof, which is additional to the dynamic proxy-based proof that already ran", err, stderr.String())
	}
}

// runNiseInIsolatedNetworkNamespace execs the built nise binary inside a
// fresh, interface-less user+network namespace via unshare(1).
func runNiseInIsolatedNetworkNamespace(t *testing.T, dir string, args ...string) runResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	unshareArgs := append([]string{"--user", "--net", "--map-root-user", "--", niseBinary}, args...)
	// #nosec G204 -- niseBinary is a path this test built; args are this
	// package's own fixed test cases.
	cmd := exec.CommandContext(ctx, "unshare", unshareArgs...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("nise %s (network namespace) timed out after 20s (stdout=%q stderr=%q)",
			strings.Join(args, " "), stdout.String(), stderr.String())
	}

	res := runResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.exitCode = 0
	case errors.As(runErr, &exitErr):
		res.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("nise %s (network namespace): could not run subprocess: %v (stderr=%q)", strings.Join(args, " "), runErr, stderr.String())
	}
	return res
}
