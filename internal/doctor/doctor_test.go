package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// allToolsHealthyRunner returns a fakeRunner stubbed with real-shaped,
// passing output for every tool check Run evaluates, so tests can flip
// just the one invocation they care about.
func allToolsHealthyRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{
		"go version":                    "go version go1.26.5-X:nodwarf5 linux/amd64\n",
		"node --version":                "v22.22.2\n",
		"pnpm --version":                "10.33.0\n",
		"docker --version":              "Docker version 29.6.2, build dfc4efb\n",
		"go tool sqlc version":          "v1.31.1\n",
		"go tool goose --version":       "goose version: v3.27.3\n",
		"go tool oapi-codegen -version": "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen\nv2.8.0\n",
	}}
}

func TestRunAllHealthyOutsideAProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := allToolsHealthyRunner()

	report := Run(context.Background(), Options{
		Runner:      r,
		Timeout:     time.Second,
		WorkDir:     dir,
		NiseVersion: "v0.1.0",
	})

	if report.Failed() {
		t.Fatalf("report.Failed() = true, want false; checks: %+v", report.Checks)
	}
	if len(report.Checks) != 9 {
		t.Fatalf("len(Checks) = %d, want 9", len(report.Checks))
	}

	wantOrder := []string{
		"go", "node", "pnpm", "container-runtime", "sqlc", "goose", "oapi-codegen",
		CheckRecipeName, CheckRecipeCompatibilityName,
	}
	for i, name := range wantOrder {
		if report.Checks[i].Name != name {
			t.Errorf("Checks[%d].Name = %q, want %q (order must be deterministic)", i, report.Checks[i].Name, name)
		}
	}

	// Outside any generated project, both recipe checks must skip, not
	// silently pass.
	if report.Checks[7].Status != StatusSkipped {
		t.Errorf("recipe check Status = %v, want %v", report.Checks[7].Status, StatusSkipped)
	}
	if report.Checks[8].Status != StatusSkipped {
		t.Errorf("recipe-compatibility check Status = %v, want %v", report.Checks[8].Status, StatusSkipped)
	}
}

func TestRunOneMissingToolFailsTheWholeReport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := allToolsHealthyRunner()
	r.errs = map[string]error{"pnpm --version": errNotFound("pnpm")}

	report := Run(context.Background(), Options{Runner: r, Timeout: time.Second, WorkDir: dir, NiseVersion: "v0.1.0"})

	if !report.Failed() {
		t.Fatal("report.Failed() = false, want true when a required tool is missing")
	}
}

func TestRunInsideAGeneratedProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := recipe.Marshal(recipe.Recipe{
		SchemaVersion:  recipe.SchemaVersion,
		Profile:        recipe.ProfileGoChiPostgresSvelte,
		CLIVersion:     "v0.1.0",
		RuntimeVersion: "v0.1.0",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	writeRecipeFile(t, root, data)

	r := allToolsHealthyRunner()
	report := Run(context.Background(), Options{Runner: r, Timeout: time.Second, WorkDir: nested, NiseVersion: "v0.1.0"})

	if report.Failed() {
		t.Fatalf("report.Failed() = true, want false; checks: %+v", report.Checks)
	}
	for _, c := range report.Checks {
		if c.Name == CheckRecipeName || c.Name == CheckRecipeCompatibilityName {
			if c.Status != StatusOK {
				t.Errorf("%s check Status = %v, want %v (Found=%q)", c.Name, c.Status, StatusOK, c.Found)
			}
		}
	}
}

// TestRunNeverInvokesAnythingOutsideItsFixedToolList is doctor's own local
// guard for constraints.md §4 ("No network, no telemetry"): Run must only
// ever invoke the handful of local tool-version commands this package
// defines, never anything else — in particular nothing that looks like it
// could reach a registry or update endpoint. A later task (M1-010) proves
// this for the whole binary at the process level; this test proves the
// narrower claim that this package's own call list is exactly what it
// claims to be.
func TestRunNeverInvokesAnythingOutsideItsFixedToolList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := allToolsHealthyRunner()

	Run(context.Background(), Options{Runner: r, Timeout: time.Second, WorkDir: dir, NiseVersion: "v0.1.0"})

	allowed := map[string]bool{
		"go version":                    true,
		"node --version":                true,
		"pnpm --version":                true,
		"docker --version":              true,
		"go tool sqlc version":          true,
		"go tool goose --version":       true,
		"go tool oapi-codegen -version": true,
	}
	for _, call := range r.calls {
		if !allowed[call] {
			t.Errorf("Run invoked %q, which is not one of doctor's documented local checks", call)
		}
	}
}

type errNotFound string

func (e errNotFound) Error() string {
	return `exec: "` + string(e) + `": executable file not found in $PATH`
}
