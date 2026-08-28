package nonetwork

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// repoRoot returns this module's root directory, resolved through the go
// tool rather than assumed from the test's own working directory (which is
// this package's directory, test/nonetwork, under `go test`) or hard-coded
// as a relative "../..", which would silently break if this package ever
// moved.
func repoRoot(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -m -f {{.Dir}}: %v\n%s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}
