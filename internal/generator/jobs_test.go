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

// TestTheRetryContractIsExplicitAndJittered pins M8-003's decisions: the
// generated client does not inherit River's retry defaults, and the schedule
// it does use is jittered.
func TestTheRetryContractIsExplicitAndJittered(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	retry := content["internal/platform/jobs/retry.go"]
	for _, fragment := range []string{
		"DefaultMaxAttempts = 12",
		"RetryBaseDelay = 2 * time.Second",
		"RetryFactor = 3",
		"RetryMaxDelay = 30 * time.Minute",
		"DefaultJobTimeout = time.Minute",
		"func (p *RetryPolicy) NextRetry(job *rivertype.JobRow) time.Time",
		"func Terminal(err error) error",
		"func Snooze(d time.Duration) error",
		"func IsTerminal(err error) bool",
	} {
		if !strings.Contains(retry, fragment) {
			t.Errorf("internal/platform/jobs/retry.go lacks %q", fragment)
		}
	}

	// The delay must be randomized. A schedule without this is the crowd
	// that turns a recovery into a second outage.
	if !strings.Contains(retry, "p.randomFloat()") {
		t.Error("the retry delay is not jittered")
	}
	// And it must keep a floor: a retry landing immediately after an outage
	// is the worst possible moment for it to arrive.
	if !strings.Contains(retry, "half := time.Duration(delay / 2)") {
		t.Error("the jitter has no floor; a retry could be scheduled for right now")
	}

	// The client has to actually adopt the policy, or every constant above
	// is documentation.
	jobs := content["internal/platform/jobs/jobs.go"]
	for _, fragment := range []string{
		"JobTimeout:  DefaultJobTimeout,",
		"MaxAttempts: DefaultMaxAttempts,",
		"RetryPolicy: NewRetryPolicy(),",
	} {
		if !strings.Contains(jobs, fragment) {
			t.Errorf("the generated client does not adopt the retry contract: missing %q", fragment)
		}
	}

	// The schedule's own test must freeze the clock. Read from the real
	// one, a spread assertion passes against a policy with no jitter at all
	// — which is how it was first written here.
	retryTest := content["internal/platform/jobs/retry_test.go"]
	if !strings.Contains(retryTest, "TestJitterSpreadsARetryCrowd") {
		t.Fatal("the generated project does not test that retries are spread")
	}
	spread := retryTest[strings.Index(retryTest, "func TestJitterSpreadsARetryCrowd"):]
	if !strings.Contains(spread[:min(len(spread), 900)], "now: func() time.Time { return fixedRetryNow }") {
		t.Error("the spread test does not freeze the clock, so it would pass with the jitter removed")
	}
}

// TestPeriodicSchedulingIsUniqueInTheDatabase pins M8-004: the uniqueness of
// a periodic job comes from a database constraint, not from which replica
// happens to be the scheduler.
func TestPeriodicSchedulingIsUniqueInTheDatabase(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())
	periodic := content["internal/platform/jobs/periodic.go"]

	for _, fragment := range []string{
		"func Periodic[T river.JobArgs](r *Registry, interval time.Duration, args T) error",
		"func PeriodicOn[T river.JobArgs](r *Registry, schedule river.PeriodicSchedule, uniquePeriod time.Duration, args T) error",
		"ByPeriod: uniquePeriod,",
		"ByState: rivertype.UniqueOptsByStateDefault(),",
		"RunOnStart: true,",
		"MinPeriodicInterval = time.Minute",
		"func (r *Registry) scheduledWithoutWorker() []string",
	} {
		if !strings.Contains(periodic, fragment) {
			t.Errorf("internal/platform/jobs/periodic.go lacks %q", fragment)
		}
	}

	// The client must adopt the schedule, and must refuse a kind it cannot
	// run. A periodic job with no worker is inserted forever and never runs,
	// which reports as nothing at all.
	jobsFile := content["internal/platform/jobs/jobs.go"]
	if !strings.Contains(jobsFile, "PeriodicJobs: registry.riverPeriodicJobs(),") {
		t.Error("the generated client does not install the periodic schedule")
	}
	if !strings.Contains(jobsFile, "registry.scheduledWithoutWorker()") {
		t.Error("the generated client does not refuse a scheduled kind with no worker")
	}

	// Both process modes report what they schedule, so "the job never ran"
	// and "this deployment does not schedule it" are distinguishable from
	// the log rather than from a database query.
	if !strings.Contains(content["internal/app/app.go"], `slog.Any("periodic_job_kinds", jobClient.PeriodicKinds())`) {
		t.Error("the startup line does not report the scheduled kinds")
	}
	if !strings.Contains(content["internal/app/modes.go"], `slog.Any("periodic_job_kinds", client.PeriodicKinds())`) {
		t.Error("the worker startup line does not report the scheduled kinds")
	}

	// A scheduling mistake must stop the process, which means the hook has
	// to be able to report one.
	if !strings.Contains(content["internal/app/jobs.go"], "func registerJobs(registry *jobs.Registry) error") {
		t.Error("registerJobs cannot report a scheduling mistake")
	}
}

// TestGeneratedProjectSendsMailSafely pins M8-005: the mail boundary, the
// transports, and the two refusals that keep a header from becoming two.
func TestGeneratedProjectSendsMailSafely(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"internal/platform/mail/mail.go": {
			"type Mailer interface {",
			"func (m Message) Validate() error",
			"func ValidateAddress(address string) error",
			`strings.ContainsAny(s, "\r\n\x00")`,
			"ErrHeaderInjection",
		},
		"internal/platform/mail/smtp.go": {
			"func (s *SMTP) Send(ctx context.Context, m Message) error",
			`client.Extension("STARTTLS")`,
			"MinVersion: tls.VersionTLS12",
			"mime.QEncoding.Encode",
			"multipart/alternative",
			"type Log struct {",
		},
		"internal/platform/mail/render.go": {
			"//go:embed templates/*.txt templates/*.html",
			"htmltemplate",
			"texttemplate",
			`Option("missingkey=error")`,
		},
		"internal/app/mail.go": {
			"func newMailer(cfg config.Config, logger *slog.Logger) (mail.Mailer, error)",
		},
		"internal/platform/config/config.go": {
			`l.String("MAIL_TRANSPORT"`,
			`l.Secret("SMTP_PASSWORD"`,
			`v.Check(cfg.MailTransport == "smtp", "MAIL_TRANSPORT"`,
		},
		".env.example": {
			"MAIL_TRANSPORT=log",
			"SMTP_ENCRYPTION=starttls",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// Nothing in the mail package may stuff a dot. net/smtp's DotWriter
	// already does, and doing it twice puts an extra dot into every message
	// whose body starts a line with one — which this package did until the
	// test asserting wire bytes caught it.
	smtp := content["internal/platform/mail/smtp.go"]
	if strings.Contains(smtp, "func dotStuff(") {
		t.Error("the mail package stuffs dots itself; net/smtp's DotWriter already does, and twice is a corrupted message")
	}

	// The plain-text alternative has to be written before the HTML one: a
	// client renders the last one it understands.
	textPart := strings.Index(smtp, `writePart(&b, boundary, `+"`"+`text/plain`)
	htmlPart := strings.Index(smtp, `writePart(&b, boundary, `+"`"+`text/html`)
	if textPart < 0 || htmlPart < 0 {
		t.Fatal("the multipart body does not write both alternatives")
	}
	if textPart > htmlPart {
		t.Error("the HTML alternative is written before the plain-text one, so no client will render the HTML")
	}

	// Both message templates ship, and both are copied verbatim: their
	// delimiters belong to the generated application, not to Nise.
	for _, path := range []string{
		"internal/platform/mail/templates/invitation.txt",
		"internal/platform/mail/templates/invitation.html",
	} {
		if !strings.Contains(content[path], "{{.AcceptURL}}") {
			t.Errorf("%s does not carry the application's own template delimiters", path)
		}
	}
}

// TestTheUploadsModuleShipsBothStorageBackends pins M8-006, including the two
// properties that make the local backend safe and the one that makes the S3
// signature verifiable.
func TestTheUploadsModuleShipsBothStorageBackends(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())

	wants := map[string][]string{
		"internal/platform/storage/storage.go": {
			"type Store interface {",
			"func ValidateKey(key string) error",
			"func SanitizeFilename(name string) string",
			"if path.Clean(key) != key {",
			"func allowedKeyRune(r rune) bool",
		},
		"internal/platform/storage/local.go": {
			"os.OpenRoot(dir)",
			"localFileMode fs.FileMode = 0o600",
			"localDirMode  fs.FileMode = 0o700",
			"file.Chmod(localFileMode)",
			"l.root.Rename(temporary, key)",
		},
		"internal/platform/storage/sigv4.go": {
			`sigV4Algorithm = "AWS4-HMAC-SHA256"`,
			"func canonicalURI(u *url.URL) string",
			"func uriEncode(s string, encodeSlash bool) string",
			`values := map[string]string{"host": hostOf(req)}`,
		},
		"internal/platform/storage/s3.go": {
			"func NewS3Settings(",
			"func (s *S3) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error)",
			"pathStyle",
		},
		"internal/app/storage.go": {
			"func newStore(cfg config.Config) (storage.Store, func() error, error)",
		},
		"internal/platform/config/config.go": {
			`l.String("STORAGE_BACKEND"`,
			`l.Secret("S3_SECRET_ACCESS_KEY"`,
		},
		".env.example": {
			"STORAGE_BACKEND=local",
			"S3_PATH_STYLE=true",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The local backend must reach the filesystem only through the confined
	// root. A single os.OpenFile or os.Remove taken on the base path would
	// undo the confinement for that one operation, which is all it takes.
	local := content["internal/platform/storage/local.go"]
	for _, escape := range []string{"os.OpenFile(", "os.Remove(", "os.Rename(", "os.Open("} {
		if strings.Contains(local, escape) {
			t.Errorf("the local backend calls %s directly, outside the confined root", escape)
		}
	}
}

// TestAProjectWithoutUploadsHasNoStorageAtAll is the module rule, applied to
// this module: absent, not disabled.
func TestAProjectWithoutUploadsHasNoStorageAtAll(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "internal/platform/storage/") || f.Path == "internal/app/storage.go" {
			t.Errorf("a project without the uploads module still contains %s", f.Path)
		}
		if strings.Contains(string(f.Content), "STORAGE_BACKEND") {
			t.Errorf("%s mentions a storage variable in a project without the uploads module", f.Path)
		}
	}
}

// TestTheUploadLifecycleTrustsNothingTheClientSent pins M8-007. Every
// assertion here is a decision that would be invisible if it were only a
// comment.
func TestTheUploadLifecycleTrustsNothingTheClientSent(t *testing.T) {
	t.Parallel()

	content := planContent(t, allModulesOptions())
	lifecycle := content["internal/features/uploads/uploads.go"]

	for _, fragment := range []string{
		// The key is derived from randomness this package generates, never
		// from anything the client sent.
		"storage.JoinKey(QuarantinePrefix, u.keys(), filename)",
		"keys:       storage.NewObjectID,",
		// The type is sniffed from the stored bytes.
		"http.DetectContentType(head)",
		// Ownership is checked on every path that acts on an upload.
		"func (u *Uploads) load(ctx context.Context, id string, ownerUserID string) (Upload, error)",
		// And the two refusals are the same error value, so an identifier
		// is not an oracle for whether an upload exists.
		`ErrNotOwner = errors.New("uploads: no such upload")`,
		`ErrNotFound = errors.New("uploads: no such upload")`,
	} {
		if !strings.Contains(lifecycle, fragment) {
			t.Errorf("internal/features/uploads/uploads.go lacks %q", fragment)
		}
	}

	// Finalization must measure before it writes anything, and must move
	// before it marks the row available.
	finalize := lifecycle[strings.Index(lifecycle, "func (u *Uploads) Finalize("):]
	measure := strings.Index(finalize, "u.measure(ctx, upload)")
	move := strings.Index(finalize, "u.store.Move(")
	mark := strings.Index(finalize, "MarkUploadAvailable")
	if measure < 0 || move < 0 || mark < 0 {
		t.Fatal("Finalize does not measure, move, and mark")
	}
	if measure >= move || move >= mark {
		t.Error("Finalize's order is wrong: it must measure, then move out of quarantine, then mark the row available")
	}

	// The declared values are recorded beside the measured ones, so a lie
	// is visible rather than merely refused.
	migration := content["db/migrations/00011_uploads.sql"]
	if migration == "" {
		t.Fatal("the uploads migration is not numbered after TOTP's")
	}
	for _, column := range []string{"declared_type", "declared_size", "content_type", "size", "checksum"} {
		if !strings.Contains(migration, column) {
			t.Errorf("the uploads table has no %s column", column)
		}
	}
	// The state and the columns describing it cannot disagree, because the
	// database will not store a row in which they do.
	if !strings.Contains(migration, "CONSTRAINT uploads_finalized_state CHECK") {
		t.Error("nothing stops a row from claiming to be available with no size or checksum")
	}

	// The sweep is a periodic job, so an abandoned upload does not occupy a
	// quarantine key forever.
	sweep := content["internal/features/uploads/sweep.go"]
	if !strings.Contains(sweep, "jobs.Periodic(registry, SweepInterval, SweepArgs{})") {
		t.Error("the sweep is not scheduled")
	}
	if !strings.Contains(content["internal/app/jobs.go"], "uploads.RegisterSweep(registry, uploadSweep, logger)") {
		t.Error("the application does not register the sweep")
	}
}
