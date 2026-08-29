package generator_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDefinesSupportedMigrationMatrix(t *testing.T) {
	t.Parallel()
	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byPath := make(map[string]string, len(files))
	for _, file := range files {
		byPath[file.Path] = string(file.Content)
	}
	for path, contracts := range map[string][]string{
		"internal/platform/database/migration_matrix_test.go": {
			"TestMigrationMatrixCleanInstall",
			"TestMigrationMatrixSupportedUpgrades",
			"migration-upgrades/*.sql",
		},
		"internal/platform/database/testdata/migration-upgrades/00001_baseline.sql": {
			"CREATE TABLE goose_db_version",
			"VALUES (0, true), (1, true)",
		},
	} {
		content, ok := byPath[path]
		if !ok {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, contract := range contracts {
			if !strings.Contains(content, contract) {
				t.Errorf("%s lacks %q", path, contract)
			}
		}
	}
}

func TestCIRunsGeneratedMigrationMatrixAgainstPostgres(t *testing.T) {
	t.Parallel()
	workflow, err := os.ReadFile(filepath.Join(findFrameworkRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	for _, contract := range []string{
		"migration-test:",
		"image: postgres:17.6-alpine",
		"make migration-test",
		"TEST_DATABASE_URL:",
		"TEST_DATABASE_PASSWORD:",
	} {
		if !strings.Contains(string(workflow), contract) {
			t.Errorf("CI workflow lacks %q", contract)
		}
	}
}

func TestGeneratedMigrationMatrixAgainstPostgres(t *testing.T) {
	if os.Getenv("NISE_MIGRATION_MATRIX") != "1" {
		t.Skip("set NISE_MIGRATION_MATRIX=1 through make migration-test to run the generated PostgreSQL matrix")
	}
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Fatal("TEST_DATABASE_URL is required for the generated PostgreSQL migration matrix")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find go: %v", err)
	}
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Fatalf("find make: %v", err)
	}

	root := filepath.Join(t.TempDir(), "migrationprobe")
	if _, err := generator.Write(root, generator.Options{
		Name:       "migrationprobe",
		ModulePath: "example.com/migrationprobe",
		CLIVersion: fixedVersion,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	run := func(bin string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- binaries and argv are fixed by this test.
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod -buildvcs=false")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
		}
	}
	run(goBin, "mod", "edit", "-replace", generator.NiseModulePath+"="+findFrameworkRoot(t))
	run(makeBin, "migration-test")
}
