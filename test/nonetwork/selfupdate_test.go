package nonetwork

// Nise never updates itself and keeps no state of its own. The rest of this
// package proves nise does not reach the network; these tests prove the two
// things a *self-updater* and a *telemetry client* need even before a socket
// is involved.
//
// A self-updater has to replace the running binary. A telemetry client has to
// buffer events somewhere, and an update checker has to remember when it last
// looked — both of which mean a file under the user's home directory. So the
// claims here are mechanical rather than about intent: after running every
// non-interactive command, the binary is byte-identical to the one that
// started, and nothing named for nise exists anywhere in a fresh home
// directory.
//
// They are deliberately checks on the *artifact*, not on the source. A grep
// for "update" in Go files proves nothing about a compiled binary; running the
// binary and looking at the filesystem afterwards does.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// selfReplacementMechanisms are the standard-library calls a binary needs in
// order to replace, re-exec, or relocate itself. None of them is forbidden in
// principle — each has ordinary uses — but nise has no reason to reach for one,
// so each appearance must be justified here rather than discovered later.
//
// This is the static half. It cannot see a mechanism reached through a
// dependency or built at runtime, which is why the dynamic checks below exist
// as a separate, independent layer.
var selfReplacementMechanisms = []string{
	"os.Executable",
	"syscall.Exec",
	"os.UserHomeDir",
	"os.UserConfigDir",
	"os.UserCacheDir",
}

// selfReplacementAllowlist names the production source lines permitted to use
// one of the mechanisms above, and why. A path is matched by suffix so the
// table stays readable.
var selfReplacementAllowlist = map[string]string{
	// An atomic write of the backup file the operator named: write to a
	// temporary path in the same directory, then rename over the target, so a
	// crash mid-dump cannot leave a truncated file that looks complete. It
	// renames a backup onto a backup, never a binary onto a binary.
	"templates/new/internal/platform/backup/operations.go.tmpl": "os.Rename for an atomic backup-file write; the source and target are both the operator's named backup path",

	// The update check has to know how nise was installed in order to print
	// the one instruction that will work — `brew upgrade` on a binary
	// Homebrew does not own does nothing and reads like a bug in nise. The
	// binary's own path is what distinguishes a Homebrew installation from a
	// downloaded archive, since they are the same bytes.
	//
	// It **reads** the path and nothing else. The dynamic proofs in this file
	// are what actually keep that true: every command runs from a private
	// copy of the binary that is hashed before and after, `nise version
	// check` included.
	"internal/release/channel.go": "os.Executable to read the running binary's own path, so the update check can name the installation channel; the path is read, never written, and TestNiseNeverModifiesItsOwnBinary covers this command like every other",
}

// TestNoSelfReplacementMechanism greps production source for the calls a
// self-updater needs.
func TestNoSelfReplacementMechanism(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, path := range goSourceFiles(t, scanRoots) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativizing %s: %v", path, err)
		}
		relative = filepath.ToSlash(relative)
		if _, allowed := selfReplacementAllowlist[relative]; allowed {
			continue
		}

		lines, err := readLines(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for i, line := range lines {
			for _, mechanism := range selfReplacementMechanisms {
				if strings.Contains(line, mechanism) {
					t.Errorf("%s:%d uses %s, which is a mechanism a self-updater or a state-keeping client needs: %s\n"+
						"If this is legitimate, add it to selfReplacementAllowlist with the reason.",
						relative, i+1, mechanism, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestSelfReplacementAllowlistHasNoStaleEntries keeps the table above honest.
// An entry for a file that no longer uses a mechanism is a permission nobody
// reviewed still standing.
func TestSelfReplacementAllowlistHasNoStaleEntries(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for path, reason := range selfReplacementAllowlist {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) // #nosec G304 -- a path from this file's own table.
		if err != nil {
			t.Errorf("selfReplacementAllowlist names %s (%s), which does not exist", path, reason)
			continue
		}
		if !strings.Contains(string(data), "os.Rename") {
			var used bool
			for _, mechanism := range selfReplacementMechanisms {
				if strings.Contains(string(data), mechanism) {
					used = true
					break
				}
			}
			if !used {
				t.Errorf("selfReplacementAllowlist names %s, which no longer uses any listed mechanism; remove the entry", path)
			}
		}
	}
}

// TestNiseNeverModifiesItsOwnBinary runs every non-interactive command from a
// private copy of the binary and checks the copy afterwards.
//
// This is the claim "nise does not self-update", reduced to something a test
// can observe. A self-updater's whole purpose is to end with different bytes
// on disk than it started with — so if the bytes never change, no command has
// one, whatever the source says.
func TestNiseNeverModifiesItsOwnBinary(t *testing.T) {
	for _, c := range dynamicCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			binDir := t.TempDir()
			name := "nise"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			copied := filepath.Join(binDir, name)
			copyBinary(t, niseBinary, copied)

			before := hashFile(t, copied)
			beforeInfo, err := os.Stat(copied)
			if err != nil {
				t.Fatalf("stat before: %v", err)
			}

			runFrom(t, copied, t.TempDir(), isolatedHomeEnv(home), c.args...)

			if after := hashFile(t, copied); after != before {
				t.Errorf("nise %s changed its own binary: %s before, %s after — this is what a self-update looks like",
					strings.Join(c.args, " "), before[:12], after[:12])
			}
			afterInfo, err := os.Stat(copied)
			if err != nil {
				t.Fatalf("stat after: %v", err)
			}
			// The size and mode are checked separately from the hash so that a
			// same-length, same-content rewrite that only changed permissions
			// (the shape of "make it writable, replace it later") is still
			// visible.
			if afterInfo.Size() != beforeInfo.Size() || afterInfo.Mode() != beforeInfo.Mode() {
				t.Errorf("nise %s changed its own binary's size or mode: %d/%v before, %d/%v after",
					strings.Join(c.args, " "),
					beforeInfo.Size(), beforeInfo.Mode(), afterInfo.Size(), afterInfo.Mode())
			}

			// And nothing else appeared beside it — a downloaded replacement
			// staged for the next run would.
			entries, err := os.ReadDir(binDir)
			if err != nil {
				t.Fatalf("reading the binary's directory: %v", err)
			}
			if len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("nise %s left %v beside its own binary; a staged replacement looks exactly like this",
					strings.Join(c.args, " "), names)
			}
		})
	}
}

// TestNiseKeepsNoStateInTheUserEnvironment runs every non-interactive command
// with a fresh, empty home directory and checks that nothing named for nise
// appears in it.
//
// An update checker has to remember when it last looked, or it checks on every
// invocation and is not a checker so much as a beacon. A telemetry client has
// to buffer events between flushes. Both need a file somewhere durable, and on
// every platform this project supports that somewhere is under the home
// directory. Nise having no such file is a stronger statement than any
// promise about what its code does with a socket.
//
// Paths a *spawned tool* creates are a different claim — `nise doctor` runs
// `go version`, and the Go toolchain writes its own cache — so the assertion
// is specifically that nothing **named for nise** exists, not that the home
// directory is untouched. That distinction is the same one docs/no-telemetry.md
// draws for container image pulls.
func TestNiseKeepsNoStateInTheUserEnvironment(t *testing.T) {
	for _, c := range dynamicCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			runFrom(t, niseBinary, t.TempDir(), isolatedHomeEnv(home), c.args...)

			var found []string
			err := filepath.WalkDir(home, func(path string, _ os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				relative, relErr := filepath.Rel(home, path)
				if relErr != nil {
					return relErr
				}
				if strings.Contains(strings.ToLower(relative), "nise") {
					found = append(found, filepath.ToSlash(relative))
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walking the isolated home directory: %v", err)
			}
			if len(found) > 0 {
				t.Errorf("nise %s created %v under a fresh home directory; nise keeps no state of its own, and an update-check timestamp or a telemetry buffer is exactly what would appear here",
					strings.Join(c.args, " "), found)
			}
		})
	}
}

// TestTheBuiltBinaryLinksNoTelemetryModule reads the module graph the Go
// toolchain embeds in the binary itself, rather than the import graph of the
// source tree.
//
// static_test.go constrains what this repository's packages import.
// telemetry_test.go greps its source text. Neither reads the artifact. `go
// version -m` does — it reports every module actually linked in, including one
// pulled in transitively by a dependency nobody read, which is how an
// analytics SDK realistically arrives.
func TestTheBuiltBinaryLinksNoTelemetryModule(t *testing.T) {
	t.Parallel()

	// #nosec G204 -- niseBinary is a path this package built in TestMain.
	out, err := exec.Command("go", "version", "-m", niseBinary).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m %s: %v\n%s", niseBinary, err, out)
	}
	graph := string(out)
	if !strings.Contains(graph, modulePath) {
		t.Fatalf("go version -m did not report a module graph for the built binary, so this test proves nothing:\n%s", graph)
	}

	lowered := strings.ToLower(graph)
	for _, marker := range telemetryMarkers {
		if strings.Contains(lowered, marker) {
			t.Errorf("the built nise binary links a module matching %q; `go version -m` reports:\n%s", marker, graph)
		}
	}
}

// isolatedHomeEnv points every home-directory variable this project's
// platforms use at one empty directory, so a write anywhere a program would
// conventionally keep state lands somewhere this test can see.
func isolatedHomeEnv(home string) []string {
	env := append([]string{}, os.Environ()...)
	var kept []string
	overridden := map[string]bool{
		"HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true,
		"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true,
	}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if !overridden[strings.ToUpper(key)] {
			kept = append(kept, kv)
		}
	}
	return append(kept,
		"HOME="+home,
		"USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
	)
}

// runFrom execs a named binary rather than the package-level one, so the
// self-modification test can run a private copy. Its result is deliberately
// ignored: these tests assert what happened to the filesystem, and a command
// that exits non-zero for a local reason (doctor on a machine without Node)
// is still a command that must not have rewritten itself.
func runFrom(t *testing.T, binary, dir string, env []string, args ...string) {
	t.Helper()

	// #nosec G204 -- binary is a path this package built or copied; args come
	// from this package's own fixed dynamicCases table.
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	_ = cmd.Run()
}

func copyBinary(t *testing.T, from, to string) {
	t.Helper()

	source, err := os.Open(from) // #nosec G304 -- a path this package built in TestMain.
	if err != nil {
		t.Fatalf("opening the built binary: %v", err)
	}
	defer func() { _ = source.Close() }()

	info, err := source.Stat()
	if err != nil {
		t.Fatalf("stat on the built binary: %v", err)
	}
	// #nosec G304 G703 -- to is under this test's own t.TempDir().
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		t.Fatalf("creating the binary copy: %v", err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatalf("copying the binary: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Fatalf("closing the binary copy: %v", err)
	}
}

func hashFile(t *testing.T, path string) string {
	t.Helper()

	file, err := os.Open(path) // #nosec G304 -- a path under this test's own t.TempDir().
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		t.Fatalf("hashing %s: %v", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// TestAGeneratedApplicationCarriesNoTelemetry scans a real generated project.
//
// telemetry_test.go greps templates/, which is the input. This greps the
// output, which is what a person actually receives — and the two are not the
// same file set: internal/generator composes, renames, and writes files that
// exist in no template, and a generated project's frontend manifest names
// dependencies that no .go or .tmpl file mentions.
func TestAGeneratedApplicationCarriesNoTelemetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runFrom(t, niseBinary, dir, isolatedHomeEnv(t.TempDir()),
		"new", "telemetrycheck", "--module-path", "example.com/telemetrycheck", "--yes", "--quiet")

	project := filepath.Join(dir, "telemetrycheck")
	if _, err := os.Stat(filepath.Join(project, "nise.json")); err != nil {
		t.Fatalf("generation did not produce a project, so this test proves nothing: %v", err)
	}

	// The walk collects names and the reads happen afterwards, through an
	// os.Root scoped to the generated project. Nothing hostile is expected in
	// a tree this test just created, but reading a path a directory walk
	// handed you is the shape of a symlink-traversal defect, and a root-scoped
	// read is not.
	var names []string
	err := filepath.WalkDir(project, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(project, path)
		if relErr != nil {
			return relErr
		}
		names = append(names, relative)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the generated project: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no generated files were found, so this test proves nothing")
	}

	root, err := os.OpenRoot(project)
	if err != nil {
		t.Fatalf("opening the generated project: %v", err)
	}
	defer func() { _ = root.Close() }()

	for _, relative := range names {
		file, openErr := root.Open(relative)
		if openErr != nil {
			t.Fatalf("opening %s: %v", relative, openErr)
		}
		data, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil {
			t.Fatalf("reading %s: %v", relative, readErr)
		}
		lowered := strings.ToLower(string(data))
		for _, marker := range telemetryMarkers {
			if strings.Contains(lowered, marker) {
				t.Errorf("the generated project's %s matches the telemetry marker %q",
					filepath.ToSlash(relative), marker)
			}
		}
	}
	t.Logf("scanned %d generated files for telemetry markers", len(names))
}
