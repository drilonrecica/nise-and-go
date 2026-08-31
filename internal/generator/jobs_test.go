package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// TestGeneratedProjectRunsJobsOnPostgreSQL pins the shape of the job system
// every generated project starts with: River, reached through pgx, with its
// schema in the project's own migration history rather than in a second
// migrator's.
func TestGeneratedProjectRunsJobsOnPostgreSQL(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"go.mod": {
			"github.com/riverqueue/river " + generator.RiverVersion,
			"github.com/riverqueue/river/riverdriver/riverpgxv5 " + generator.RiverVersion,
		},
		"db/migrations/00009_jobs.sql": {
			"CREATE TYPE river_job_state AS ENUM",
			"CREATE TABLE river_job (",
			"CREATE UNLOGGED TABLE river_leader (",
			"CREATE TABLE river_queue (",
			"CREATE TABLE river_notification (",
			"unique_states bit(8)",
			"CREATE UNIQUE INDEX river_job_unique_idx",
			"river_job_state_in_bitmask",
			"-- +goose StatementBegin",
			"-- +goose Down",
		},
		"internal/platform/jobs/jobs.go": {
			"func New(pool *pgxpool.Pool, registry *Registry, settings Settings, logger *slog.Logger) (*Client, error)",
			"func Register[T river.JobArgs](r *Registry, worker river.Worker[T])",
			"func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error",
			"riverpgxv5.New(pool)",
		},
		"internal/platform/config/config.go": {
			"Jobs jobs.Settings",
			`l.Int("JOB_MAX_WORKERS"`,
			`l.Duration("JOB_POLL_INTERVAL"`,
		},
		"internal/app/app.go": {
			"jobs.NewRegistry()",
			"registerJobs(jobRegistry)",
			"jobs.New(pool, jobRegistry, cfg.Jobs, logger)",
			"newWorker(jobClient, logger)",
		},
		"internal/app/jobs.go": {
			"func registerJobs(registry *jobs.Registry)",
		},
		"internal/app/modes.go": {
			"client.Start(ctx)",
			"client.Stop(drainCtx)",
		},
		".env.example": {
			"JOB_MAX_WORKERS=10",
			"JOB_POLL_INTERVAL=5s",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}

// TestTheJobSchemaIsInTheProjectsOwnMigrationHistory is the decision this
// task actually made, stated as a test.
//
// River ships a migrator. Using it would put part of the schema outside the
// one history an operator reviews before a deploy, outside the explicit
// migration command, and outside the compatibility check that requires the
// version sequence to have no gaps. The schema is therefore a numbered
// migration like every other, and nothing generated imports rivermigrate.
func TestTheJobSchemaIsInTheProjectsOwnMigrationHistory(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".go") && strings.Contains(string(f.Content), "rivermigrate") {
			t.Errorf("%s imports River's own migrator; the job schema belongs to this project's migration history", f.Path)
		}
	}

	content := planContent(t, defaultOptions())
	migration := content["db/migrations/00009_jobs.sql"]
	if migration == "" {
		t.Fatal("no job migration was generated")
	}
	// The recorded line has to match the squashed statements, or River's
	// own tooling would later re-run work this migration already did.
	if !strings.Contains(migration, "SELECT 'main', generate_series(1, 7)") {
		t.Error("the migration does not record which River migration line versions it applied")
	}
	if !strings.Contains(migration, "CREATE TABLE river_migration") {
		t.Error("the migration does not create River's own bookkeeping table")
	}
}

// TestJobMigrationIsNumberedAfterTheCoreHistory keeps the module migration
// numbering honest: modules number contiguously after the core migrations,
// so a core migration added without moving that boundary would collide with
// the first module migration.
func TestJobMigrationIsNumberedAfterTheCoreHistory(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())

	if content["db/migrations/00009_jobs.sql"] == "" {
		t.Fatal("the job migration is not numbered 00009")
	}
	if content["db/migrations/00010_totp.sql"] == "" {
		t.Error("the TOTP module migration does not follow the core history at 00010")
	}
}
