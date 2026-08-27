package generator_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// TestGeneratedGoCodeBuilds runs the real toolchain against a real
// generated project. It is the test that would have caught every template
// mistake the golden manifests cannot see: an import that does not resolve,
// a runtime/ API called with the wrong signature, a package name that does
// not match its directory.
//
// It needs the network, because the generated go.mod requires
// github.com/go-chi/chi/v5 and generation deliberately writes no go.sum. It
// also needs a local replace directive, because the framework module has no
// published version carrying runtime/ yet; the replace is a property of
// this test, never of a generated project. Either being unavailable is a
// skip with the reason, not a failure.
func TestGeneratedGoCodeBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: this test runs the Go toolchain and needs the module proxy")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("skipping: no go binary on PATH: %v", err)
	}

	frameworkRoot := findFrameworkRoot(t)
	root := filepath.Join(t.TempDir(), "buildprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "buildprobe",
		ModulePath: "example.com/buildprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	runEnv := func(extraEnv []string, args ...string) (string, error) {
		cmd := exec.Command(goBin, args...) // #nosec G204 -- args are literals from this test.
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
		cmd.Env = append(cmd.Env, extraEnv...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	run := func(args ...string) (string, error) {
		return runEnv(nil, args...)
	}

	if out, err := run("mod", "edit", "-replace",
		generator.NiseModulePath+"="+frameworkRoot); err != nil {
		t.Fatalf("go mod edit -replace: %v\n%s", err, out)
	}

	if out, err := run("mod", "tidy"); err != nil {
		t.Skipf("skipping: go mod tidy could not resolve the generated project's dependencies, most likely because the module proxy is unreachable: %v\n%s", err, out)
	}

	if out, err := run("build", "./cmd/...", "./internal/..."); err != nil {
		t.Fatalf("go build of the generated project failed: %v\n%s", err, out)
	}
	if out, err := run("vet", "./cmd/...", "./internal/..."); err != nil {
		t.Fatalf("go vet of the generated project failed: %v\n%s", err, out)
	}

	// The generated project must build on every platform Nise supports,
	// not merely on the one generating it. cmd/<app>/main.go references
	// syscall.SIGTERM, which happens to be defined on Windows — but a
	// future template reaching for a Unix-only symbol would break every
	// generated project on that platform, and a runtime GOOS guard would
	// not save it, because the reference still has to resolve at compile
	// time. Cross-compiling here is what turns that from a thing someone
	// has to remember into a thing the suite checks.
	for _, target := range []string{"windows", "darwin"} {
		if out, err := runEnv([]string{"GOOS=" + target}, "build", "./cmd/...", "./internal/..."); err != nil {
			t.Errorf("the generated project does not build for GOOS=%s: %v\n%s", target, err, out)
		}
	}

	// The committed placeholder is the whole reason the build above
	// succeeded without pnpm ever having run. Assert it is embedded rather
	// than trusting that it was.
	if _, err := os.Stat(filepath.Join(root, "internal", "platform", "webui", "embedded", "placeholder.html")); err != nil {
		t.Errorf("the committed embed placeholder is missing: %v", err)
	}
}

// TestGeneratedGoCodeIsGofmtClean checks the generated Go source against
// the formatter, so a template whose indentation drifted produces a project
// that is already unformatted on its first commit.
func TestGeneratedGoCodeIsGofmtClean(t *testing.T) {
	t.Parallel()

	gofmtBin, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skipf("skipping: no gofmt binary on PATH: %v", err)
	}

	root := filepath.Join(t.TempDir(), "fmtprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "fmtprobe",
		ModulePath: "example.com/fmtprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	cmd := exec.Command(gofmtBin, "-s", "-l", "cmd", "internal") // #nosec G204 -- args are literals from this test.
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("the generated project is not gofmt -s clean:\n%s", out)
	}
}

// findFrameworkRoot walks up from the test's working directory to the
// repository root, identified by the go.mod declaring the framework module.
func findFrameworkRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) // #nosec G304 -- dir is derived from this test's own location.
		if err == nil && strings.Contains(string(data), "module "+generator.NiseModulePath) {
			return dir
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reading go.mod in %s: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find the repository root above %s", dir)
		}
		dir = parent
	}
}
