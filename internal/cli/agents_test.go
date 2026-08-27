package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/agentsfile"
	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// writeTestRecipe writes a valid, minimal nise.json into dir so agentsCommand
// finds a project root there.
func writeTestRecipe(t *testing.T, dir string) {
	t.Helper()
	r := recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		Modules:        []recipe.Module{recipe.ModuleTOTP},
		CLIVersion:     "v0.1.0",
		RuntimeVersion: "v0.1.0",
	}
	if err := recipe.Save(filepath.Join(dir, recipe.FileName), r); err != nil {
		t.Fatalf("recipe.Save: %v", err)
	}
}

func runAgentsFixture(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, []*Command{agentsCommand()})
	return code, out.String(), errOut.String()
}

func TestAgentsCommandNoRecipeIsAPreconditionError(t *testing.T) {
	// Not parallel: relies on process-wide cwd via t.Chdir.
	t.Chdir(t.TempDir())

	code, stdout, stderr := runAgentsFixture(t, []string{"agents"})
	if code != int(clierr.ExitPrecondition) {
		t.Errorf("exit code = %d, want %d (ExitPrecondition)", code, clierr.ExitPrecondition)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure in human mode", stdout)
	}
	if !strings.Contains(stderr, "nise.json") {
		t.Errorf("stderr = %q, want it to mention nise.json", stderr)
	}
}

func TestAgentsCommandNoRecipeJSONMode(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := runAgentsFixture(t, []string{"agents", "--json"})
	if code != int(clierr.ExitPrecondition) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitPrecondition)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &env); err != nil {
		t.Fatalf("stdout is not a valid JSON error envelope: %v\nstdout=%s", err, stdout)
	}
	if env.Error.Code != "agents.no_project" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "agents.no_project")
	}
}

func TestAgentsCommandWritesFilesFromCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestRecipe(t, dir)
	t.Chdir(dir)

	code, stdout, stderr := runAgentsFixture(t, []string{"agents"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty on success", stderr)
	}
	if !strings.Contains(stdout, "AGENTS.md") {
		t.Errorf("stdout = %q, want it to mention AGENTS.md", stdout)
	}

	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nise", "architecture.json")); err != nil {
		t.Errorf("architecture.json was not written: %v", err)
	}
}

func TestAgentsCommandFindsRootFromASubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestRecipe(t, dir)
	sub := filepath.Join(dir, "internal", "features", "invoice")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	code, _, stderr := runAgentsFixture(t, []string{"agents"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md was not written at the project root: %v", err)
	}
}

func TestAgentsCommandRefusesHandEditedFileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	writeTestRecipe(t, dir)
	t.Chdir(dir)

	if code, _, stderr := runAgentsFixture(t, []string{"agents"}); code != int(clierr.ExitOK) {
		t.Fatalf("first run: exit code = %d, stderr=%s", code, stderr)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("hand-edited, not nise-generated"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runAgentsFixture(t, []string{"agents"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d (ExitError)", code, clierr.ExitError)
	}
	if !strings.Contains(stdout, "hand-edited, not nise-generated") {
		t.Errorf("stdout = %q, want it to show the content that would be lost", stdout)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr = %q, want it to mention --force", stderr)
	}

	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hand-edited, not nise-generated" {
		t.Error("the refusal must not overwrite the hand-edited file")
	}
}

func TestAgentsCommandRefusesHandEditedFileJSONModeShowsDetails(t *testing.T) {
	dir := t.TempDir()
	writeTestRecipe(t, dir)
	t.Chdir(dir)

	if code, _, stderr := runAgentsFixture(t, []string{"agents", "--json"}); code != int(clierr.ExitOK) {
		t.Fatalf("first run: exit code = %d, stderr=%s", code, stderr)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("hand-edited, not nise-generated"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runAgentsFixture(t, []string{"agents", "--json"})
	if code != int(clierr.ExitError) {
		t.Errorf("exit code = %d, want %d", code, clierr.ExitError)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// No Line() prose is emitted in JSON mode, so exactly one document —
	// the error — is expected.
	if len(lines) != 1 {
		t.Fatalf("stdout had %d lines, want exactly 1 (the error document): %q", len(lines), stdout)
	}
	var env struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("stdout line is not a valid JSON error envelope: %v", err)
	}
	if env.Error.Code != "agents.conflict" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "agents.conflict")
	}
	found := false
	for _, v := range env.Error.Details {
		if strings.Contains(v, "hand-edited, not nise-generated") {
			found = true
		}
	}
	if !found {
		t.Errorf("error.details = %+v, want a value containing the hand-edited content", env.Error.Details)
	}
}

func TestAgentsCommandForceOverwritesHandEditedFile(t *testing.T) {
	dir := t.TempDir()
	writeTestRecipe(t, dir)
	t.Chdir(dir)

	if code, _, stderr := runAgentsFixture(t, []string{"agents"}); code != int(clierr.ExitOK) {
		t.Fatalf("first run: exit code = %d, stderr=%s", code, stderr)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("hand-edited, not nise-generated"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runAgentsFixture(t, []string{"agents", "--force"})
	if code != int(clierr.ExitOK) {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}

	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hand-edited, not nise-generated") {
		t.Error("--force did not overwrite the hand-edited file")
	}
}

func TestAgentsCommandInvalidRecipeIsAPreconditionError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, recipe.FileName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	code, _, stderr := runAgentsFixture(t, []string{"agents"})
	if code != int(clierr.ExitPrecondition) {
		t.Errorf("exit code = %d, want %d (ExitPrecondition)", code, clierr.ExitPrecondition)
	}
	if !strings.Contains(stderr, "nise.json") {
		t.Errorf("stderr = %q, want it to mention nise.json", stderr)
	}
}

func TestAgentsCommandHasNoReservedFlagCollisions(t *testing.T) {
	t.Parallel()
	if collisions := reservedFlagCollisions(newCommandFlagSet(agentsCommand())); len(collisions) > 0 {
		t.Errorf("agentsCommand defines colliding flag(s): %v", collisions)
	}
}

// agentsResultRoundTrip exercises agentsResult.Human directly, so its
// rendering is covered even though the fixtures above already exercise it
// indirectly through Execute.
func TestAgentsResultHuman(t *testing.T) {
	t.Parallel()
	r := agentsResult{agentsfile.Result{AgentsPath: "/p/AGENTS.md", ArchitecturePath: "/p/.nise/architecture.json"}}
	want := "wrote /p/AGENTS.md\nwrote /p/.nise/architecture.json"
	if got := r.Human(); got != want {
		t.Errorf("Human() = %q, want %q", got, want)
	}
}
