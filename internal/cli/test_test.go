package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

func runTestFixture(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, []*Command{testCommand()})
	return code, out.String(), errOut.String()
}

func writeMinimalRecipe(t *testing.T, dir string) {
	t.Helper()
	r := recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		Modules:        nil,
		CLIVersion:     "v0.1.0",
		RuntimeVersion: "v0.1.0",
	}
	if err := recipe.Save(filepath.Join(dir, recipe.FileName), r); err != nil {
		t.Fatalf("recipe.Save: %v", err)
	}
}

// --- selectedSuites ---

func TestSelectedSuitesDefaultIsGoAndComponentOnly(t *testing.T) {
	t.Parallel()
	got := selectedSuites(false, false, false)
	want := map[string]bool{suiteGo: true, suiteComponent: true, suiteE2E: false}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("selectedSuites(false,false,false)[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestSelectedSuitesExplicitFlagsAreExact(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                          string
		goFlag, componentFlag, e2e    bool
		wantGo, wantComponent, wantE2 bool
	}{
		{"e2e only", false, false, true, false, false, true},
		{"go only", true, false, false, true, false, false},
		{"go and e2e", true, false, true, true, false, true},
		{"all three", true, true, true, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := selectedSuites(tt.goFlag, tt.componentFlag, tt.e2e)
			if got[suiteGo] != tt.wantGo || got[suiteComponent] != tt.wantComponent || got[suiteE2E] != tt.wantE2 {
				t.Errorf("selectedSuites(%v,%v,%v) = %v, want go=%v component=%v e2e=%v",
					tt.goFlag, tt.componentFlag, tt.e2e, got, tt.wantGo, tt.wantComponent, tt.wantE2)
			}
		})
	}
}

// --- planGoSuite / planPackageScriptSuite: never invent a command ---

func TestPlanGoSuiteSkipsWithoutGoMod(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := planGoSuite(dir)
	if plan.Runnable {
		t.Fatal("planGoSuite reported runnable with no go.mod present")
	}
	if plan.SkipReason == "" {
		t.Error("SkipReason is empty")
	}
}

func TestPlanGoSuiteRunnableWithGoMod(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := planGoSuite(dir)
	if !plan.Runnable {
		t.Fatalf("planGoSuite reported not runnable with go.mod present: %+v", plan)
	}
	if plan.Argv[0] != "go" {
		t.Errorf("Argv[0] = %q, want %q", plan.Argv[0], "go")
	}
}

func TestPlanPackageScriptSuiteSkipsWithoutPackageJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := planPackageScriptSuite(suiteComponent, dir, componentScriptNames)
	if plan.Runnable {
		t.Fatal("reported runnable with no frontend/package.json present")
	}
	if !strings.Contains(plan.SkipReason, "package.json") {
		t.Errorf("SkipReason = %q, want it to mention package.json", plan.SkipReason)
	}
}

func TestPlanPackageScriptSuiteSkipsWhenScriptAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackageJSON(t, dir, map[string]string{"build": "vite build"})

	plan := planPackageScriptSuite(suiteComponent, dir, componentScriptNames)
	if plan.Runnable {
		t.Fatal("reported runnable with none of the candidate scripts present")
	}
	if !strings.Contains(plan.SkipReason, "test:unit") {
		t.Errorf("SkipReason = %q, want it to list the script names it looked for", plan.SkipReason)
	}
}

func TestPlanPackageScriptSuiteRunnableWhenScriptDeclared(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackageJSON(t, dir, map[string]string{"test:unit": "vitest run"})

	plan := planPackageScriptSuite(suiteComponent, dir, componentScriptNames)
	if !plan.Runnable {
		t.Fatalf("reported not runnable despite a matching script: %+v", plan)
	}
	want := []string{"pnpm", "--dir", "frontend", "run", "test:unit"}
	if len(plan.Argv) != len(want) {
		t.Fatalf("Argv = %v, want %v", plan.Argv, want)
	}
	for i := range want {
		if plan.Argv[i] != want[i] {
			t.Errorf("Argv[%d] = %q, want %q", i, plan.Argv[i], want[i])
		}
	}
}

func TestPlanPackageScriptSuitePrefersFirstCandidateInOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackageJSON(t, dir, map[string]string{"test:unit": "vitest run", "test:component": "other"})

	plan := planPackageScriptSuite(suiteComponent, dir, componentScriptNames)
	if plan.Argv[len(plan.Argv)-1] != "test:component" {
		t.Errorf("chose script %q, want the first candidate, test:component", plan.Argv[len(plan.Argv)-1])
	}
}

func writePackageJSON(t *testing.T, root string, scripts map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"scripts": scripts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "package.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- runSuite ---

// fixtureWriter is a minimal output.Writer that records every Line call,
// for asserting streamed child output without a real terminal.
type fixtureWriter struct {
	lines []string
}

func (w *fixtureWriter) Mode() output.Mode             { return output.ModeHuman }
func (w *fixtureWriter) Line(msg string)               { w.lines = append(w.lines, msg) }
func (w *fixtureWriter) Verbosef(string, ...any)       {}
func (w *fixtureWriter) Success(string)                {}
func (w *fixtureWriter) Banner(string)                 {}
func (w *fixtureWriter) Spinner(string) output.Spinner { return nil }
func (w *fixtureWriter) Result(output.Result)          {}
func (w *fixtureWriter) Error(error)                   {}

func TestRunSuiteSkippedNeverExecutes(t *testing.T) {
	t.Parallel()
	plan := suitePlan{Name: "go", Runnable: false, SkipReason: "no go.mod found"}
	w := &fixtureWriter{}
	res := runSuite(context.Background(), plan, nil, w)
	if res.Status != string(statusSkipped) {
		t.Errorf("Status = %q, want %q", res.Status, statusSkipped)
	}
	if res.Reason != "no go.mod found" {
		t.Errorf("Reason = %q, want the plan's SkipReason", res.Reason)
	}
	if len(w.lines) != 0 {
		t.Errorf("Line() was called for a skipped suite: %v", w.lines)
	}
}

func TestRunSuiteStreamsChildOutputLineByLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := suitePlan{
		Name:     "go",
		Runnable: true,
		Dir:      dir,
		Argv:     []string{"sh", "-c", "echo first; echo second"},
	}
	w := &fixtureWriter{}
	res := runSuite(context.Background(), plan, nil, w)
	if res.Status != string(statusPassed) {
		t.Fatalf("Status = %q, want %q", res.Status, statusPassed)
	}
	if len(w.lines) != 2 || w.lines[0] != "first" || w.lines[1] != "second" {
		t.Errorf("lines = %v, want [\"first\" \"second\"]", w.lines)
	}
}

func TestRunSuiteReportsFailureExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := suitePlan{Name: "go", Runnable: true, Dir: dir, Argv: []string{"sh", "-c", "exit 7"}}
	w := &fixtureWriter{}
	res := runSuite(context.Background(), plan, nil, w)
	if res.Status != string(statusFailed) {
		t.Fatalf("Status = %q, want %q", res.Status, statusFailed)
	}
	if res.ExitCode == nil || *res.ExitCode != 7 {
		t.Errorf("ExitCode = %v, want 7", res.ExitCode)
	}
}

func TestRunSuiteAppendsExtraArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := suitePlan{Name: "go", Runnable: true, Dir: dir, Argv: []string{"sh", "-c", `echo "$@"`, "--"}}
	w := &fixtureWriter{}
	res := runSuite(context.Background(), plan, []string{"hello", "world"}, w)
	if res.Status != string(statusPassed) {
		t.Fatalf("Status = %q, want %q", res.Status, statusPassed)
	}
	if len(w.lines) != 1 || w.lines[0] != "hello world" {
		t.Errorf("lines = %v, want the extra args forwarded to the child", w.lines)
	}
}

func TestRunSuiteKillsChildOnContextCancellation(t *testing.T) {
	t.Parallel()
	assertRunSuiteReturnsOnCancellation(t, "sleep 30")
}

// TestRunSuiteKillsAGrandchildOnContextCancellation is the case that
// actually happens. Nothing nise runs is a leaf: `go test` compiles and
// executes test binaries as its own children, and `pnpm test` forks a
// runner. Terminating only the process nise started leaves those behind,
// and — because they inherited the write end of the pipe runSuite reads —
// leaves runSuite blocked in Wait until they finish on their own, which is
// the entire duration the interrupt was meant to cut short.
//
// `sleep 30 & wait` is the smallest shell spelling of that shape: the shell
// is the direct child, the sleep is the grandchild holding the pipe, and no
// shell optimises the pair into a single exec. Against a runSuite that
// kills only its direct child this fails in the same five seconds and with
// the same message as its sibling above, on every platform rather than only
// where the shell happened to fork.
func TestRunSuiteKillsAGrandchildOnContextCancellation(t *testing.T) {
	t.Parallel()
	assertRunSuiteReturnsOnCancellation(t, "sleep 30 & wait")
}

// assertRunSuiteReturnsOnCancellation runs script as a suite, cancels the
// context once it is under way, and fails unless runSuite returns promptly.
func assertRunSuiteReturnsOnCancellation(t *testing.T, script string) {
	t.Helper()

	plan := suitePlan{Name: "go", Runnable: true, Dir: t.TempDir(), Argv: []string{"sh", "-c", script}}
	w := &fixtureWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runSuite(ctx, plan, nil, w)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// runSuite returned promptly after cancellation: the child tree
		// was terminated rather than left to run its full 30s sleep,
		// which is exactly what must happen so a Ctrl-C never orphans a
		// process nise started.
	case <-time.After(5 * time.Second):
		t.Fatalf("runSuite did not return within 5s of context cancellation for %q; the child tree was not terminated", script)
	}
}

func TestRunSuiteSkipsWhenContextAlreadyDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := suitePlan{Name: "e2e", Runnable: true, Dir: t.TempDir(), Argv: []string{"sh", "-c", "echo should-not-run"}}
	w := &fixtureWriter{}
	res := runSuite(ctx, plan, nil, w)
	if res.Status != string(statusSkipped) {
		t.Errorf("Status = %q, want %q (a canceled context must not start a new child)", res.Status, statusSkipped)
	}
	if len(w.lines) != 0 {
		t.Errorf("lines = %v, want no output: the child must never have started", w.lines)
	}
}

// --- end-to-end command wiring ---

func TestTestCommandNoRecipeIsAPreconditionError(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runTestFixture(t, []string{"test"})
	if code != int(clierr.ExitPrecondition) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitPrecondition)
	}
	if !strings.Contains(stderr, "nise.json") {
		t.Errorf("stderr = %q, want it to mention nise.json", stderr)
	}
}

func TestTestCommandReportsSkippedSuitesHonestly(t *testing.T) {
	dir := t.TempDir()
	writeMinimalRecipe(t, dir)
	// No go.mod, no frontend/package.json: every suite this project could
	// declare is genuinely absent.
	t.Chdir(dir)

	code, stdout, stderr := runTestFixture(t, []string{"test"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0 (an absent suite is skipped, not failed); stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "[SKIPPED] go") {
		t.Errorf("stdout = %q, want the go suite reported skipped", stdout)
	}
	if !strings.Contains(stdout, "[SKIPPED] component") {
		t.Errorf("stdout = %q, want the component suite reported skipped", stdout)
	}
	if strings.Contains(stdout, "e2e") {
		t.Errorf("stdout = %q, want no mention of e2e: it was not selected by the default flags", stdout)
	}
}

func TestTestCommandRunsRealGoSuiteAndReportsPass(t *testing.T) {
	dir := t.TempDir()
	writeMinimalRecipe(t, dir)
	writeTrivialGoModule(t, dir)
	t.Chdir(dir)

	code, stdout, stderr := runTestFixture(t, []string{"test", "--go"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "[PASSED] go") {
		t.Errorf("stdout = %q, want the go suite reported passed", stdout)
	}
}

func TestTestCommandJSONModeEmitsStructuredSummaryOnly(t *testing.T) {
	dir := t.TempDir()
	writeMinimalRecipe(t, dir)
	t.Chdir(dir)

	code, stdout, stderr := runTestFixture(t, []string{"test", "--json"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout had %d lines, want exactly 1 (the summary document): %q", len(lines), stdout)
	}
	var summary struct {
		Suites []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"suites"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &summary); err != nil {
		t.Fatalf("stdout is not a valid JSON summary document: %v", err)
	}
	if len(summary.Suites) != 2 {
		t.Fatalf("Suites = %+v, want 2 entries (go, component; e2e is not selected by default)", summary.Suites)
	}
	for _, s := range summary.Suites {
		if s.Status != "skipped" {
			t.Errorf("suite %q status = %q, want skipped (no go.mod or package.json present)", s.Name, s.Status)
		}
	}
}

func TestTestCommandFailingSuiteExitsNonZeroAndReportsAllSuites(t *testing.T) {
	dir := t.TempDir()
	writeMinimalRecipe(t, dir)
	writeFailingGoModule(t, dir)
	t.Chdir(dir)

	code, stdout, stderr := runTestFixture(t, []string{"test", "--go", "--component"})
	if code != int(clierr.ExitError) {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, clierr.ExitError, stdout, stderr)
	}
	if !strings.Contains(stdout, "[FAILED] go") {
		t.Errorf("stdout = %q, want the go suite reported failed", stdout)
	}
	if !strings.Contains(stdout, "[SKIPPED] component") {
		t.Errorf("stdout = %q, want the component suite's result still reported despite the go suite failing", stdout)
	}
	if !strings.Contains(stderr, "go") {
		t.Errorf("stderr = %q, want the error to name the failing suite", stderr)
	}
}

func TestTestCommandDoubleDashPassesArgsToGoSuiteOnly(t *testing.T) {
	dir := t.TempDir()
	writeMinimalRecipe(t, dir)
	writeTrivialGoModule(t, dir)
	t.Chdir(dir)

	code, stdout, stderr := runTestFixture(t, []string{"test", "--go", "--", "-run", "TestNothingMatches"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "[PASSED] go") {
		t.Errorf("stdout = %q, want the go suite to still report passed (matching no tests is not a failure)", stdout)
	}
}

func TestTestCommandHasNoReservedFlagCollisions(t *testing.T) {
	t.Parallel()
	if collisions := reservedFlagCollisions(newCommandFlagSet(testCommand())); len(collisions) > 0 {
		t.Errorf("testCommand defines colliding flag(s): %v", collisions)
	}
}

// writeGoFixtureModule writes a minimal Go module at dir with a package
// under each of cmd/, db/, and internal/ — planGoSuite's own three targets
// (./cmd/... ./db/... ./internal/...) — so `go test` against them finds real
// packages instead of failing at setup with "no such directory" for
// whichever a narrower fixture omitted. internal/x's own test either passes
// or fails depending on failing.
func writeGoFixtureModule(t *testing.T, dir string, failing bool) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "db", "embed.gen.go"), []byte("package db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	src := "package x\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n"
	if failing {
		src = "package x\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"boom\") }\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "x", "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTrivialGoModule(t *testing.T, dir string) { writeGoFixtureModule(t, dir, false) }

func writeFailingGoModule(t *testing.T, dir string) { writeGoFixtureModule(t, dir, true) }
