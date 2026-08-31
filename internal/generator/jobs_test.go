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
			"var _ Enqueuer = (*Client)(nil)",
			"transaction.IsActive(ctx)",
			"ErrInsertInsideTransaction",
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

// TestEnqueuingOutsideTheTransactionIsRefusedNotDocumented is the decision
// M8-002 made, stated where a future change would trip over it.
//
// The failure this prevents — a job enqueued for a change that then rolled
// back, or a change that committed with no job — is invisible in review and
// intermittent in production. Making it a comment would leave it to be
// noticed; making it a refusal makes the safe call the only one available.
func TestEnqueuingOutsideTheTransactionIsRefusedNotDocumented(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	jobs := content["internal/platform/jobs/jobs.go"]
	insert := strings.Index(jobs, "func (c *Client) Insert(")
	if insert < 0 {
		t.Fatal("the generated job client has no Insert method")
	}
	body := jobs[insert:]
	guard := strings.Index(body, "transaction.IsActive(ctx)")
	enqueue := strings.Index(body, "c.river.Insert(")
	if guard < 0 || enqueue < 0 {
		t.Fatal("Insert does not both guard and enqueue")
	}
	if guard > enqueue {
		t.Error("Insert enqueues before checking whether it is inside a transaction; the refusal must write nothing")
	}

	// The PostgreSQL proof is what makes the claim more than an assertion
	// about this project's own code.
	pg := content["internal/platform/jobs/jobs_postgres_test.go"]
	for _, name := range []string{
		"TestACommittedTransactionPublishesItsJob",
		"TestARolledBackTransactionTakesItsJobWithIt",
		"TestInsertOutsideATransactionIsImmediatelyVisible",
		"TestInsertIsRefusedInsideATransaction",
	} {
		if !strings.Contains(pg, name) {
			t.Errorf("the generated PostgreSQL job suite lacks %s", name)
		}
	}
	if !strings.Contains(pg, "SELECT count(*) FROM river_job WHERE kind = $1") {
		t.Error("the suite does not count job rows on its own connection; reading through River would prove only that River agrees with itself")
	}
}
