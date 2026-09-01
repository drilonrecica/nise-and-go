// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	migrationdb "workbench/db"
	"workbench/internal/features/resource"
	"workbench/internal/platform/database"
	"workbench/internal/platform/database/dbtest"
	"workbench/internal/platform/jobs"
)

const testTimeout = 90 * time.Second

// testClock is a fixed instant every window in these tests is relative to.
//
// A fixed clock rather than time.Now: "check out after the window opens" and
// "sweep after the window closes" are the two preconditions worth testing, and
// a test that waits for real time to pass either takes hours or does not test
// them.
var testClock = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	transactor   *database.Transactor
	reservations *Reservations
	resources    *resource.Resources
	orgID        string
	holder       string
	resource     resource.Resource
}

func newHarness(t *testing.T) *harness { return newHarnessWithPoolSize(t, 6) }

func newHarnessWithPoolSize(t *testing.T, maxConns int) *harness {
	t.Helper()

	testDatabase := dbtest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	settings, err := database.NewSettings(maxConns, 0, time.Minute, time.Minute, time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}

	migrator, err := database.OpenMigrator(ctx, testDatabase.URL().Reveal(), testDatabase.Password().Reveal(),
		5*time.Second, migrationdb.Migrations())
	if err != nil {
		t.Fatalf("OpenMigrator: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := migrator.Close(); closeErr != nil {
			t.Errorf("close migrator: %v", closeErr)
		}
	})
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// A role with no special privileges. A superuser bypasses every
	// row-level security policy, so running these as one would make every
	// isolation assertion below vacuous.
	databaseURL, databasePassword := testDatabase.Restricted(t)
	pool, err := database.Open(ctx, databaseURL, databasePassword, settings, database.OpenOptions{})
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// The premise, checked rather than assumed.
	isolation, err := database.CheckTenantIsolation(ctx, pool)
	if err != nil {
		t.Fatalf("CheckTenantIsolation: %v", err)
	}
	if !isolation.Enforced() {
		t.Fatalf("the restricted role still bypasses row-level security (%s); every isolation test below would pass vacuously", isolation.Reason())
	}

	transactor, err := database.NewTransactor(pool)
	if err != nil {
		t.Fatalf("NewTransactor: %v", err)
	}
	reservations, err := New(transactor, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resources, err := resource.New(transactor)
	if err != nil {
		t.Fatalf("resource.New: %v", err)
	}

	h := &harness{
		ctx:          ctx,
		pool:         pool,
		transactor:   transactor,
		reservations: reservations.WithClock(func() time.Time { return testClock }),
		resources:    resources,
	}
	h.orgID = h.newOrg(t, "lab")
	h.holder = h.addUser(t, "holder@example.com")
	h.resource = h.newResource(t, h.orgID, "Microscope 2")
	return h
}

// newOrg creates an organization directly, minting the identifier inside the
// transaction and establishing it as the tenant — the same thing
// Transactor.WithinNewTenant does for the organizations use case, done here so
// these tests do not depend on that package's API shape.
func (h *harness) newOrg(t *testing.T, slug string) string {
	t.Helper()

	var id string
	err := h.transactor.WithinNewTenant(h.ctx, transaction.Options{}, func(ctx context.Context, tx pgx.Tx, orgID string) error {
		_, execErr := tx.Exec(ctx, `INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)`,
			orgID, strings.ToUpper(slug)+" Ltd", slug)
		id = orgID
		return execErr
	})
	if err != nil {
		t.Fatalf("creating an organization: %v", err)
	}
	return id
}

func (h *harness) addUser(t *testing.T, email string) string {
	t.Helper()

	var id string
	if err := h.pool.QueryRow(h.ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`,
		email, strings.Repeat("x", 32)).Scan(&id); err != nil {
		t.Fatalf("creating a user: %v", err)
	}
	return id
}

func (h *harness) newResource(t *testing.T, orgID, name string) resource.Resource {
	t.Helper()

	created, err := h.resources.Create(h.ctx, orgID, resource.Details{Name: name})
	if err != nil {
		t.Fatalf("creating a resource: %v", err)
	}
	return created
}

// book makes a reservation relative to the fixed clock, in hours.
func (h *harness) book(t *testing.T, fromHours, toHours int) Reservation {
	t.Helper()

	created, err := h.reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID,
		HolderID:   h.holder,
		StartsAt:   testClock.Add(time.Duration(fromHours) * time.Hour),
		EndsAt:     testClock.Add(time.Duration(toHours) * time.Hour),
	})
	if err != nil {
		t.Fatalf("booking %+d..%+dh: %v", fromHours, toHours, err)
	}
	return created
}

// holderActor is the reservation's own holder, with no management permission.
func (h *harness) holderActor() Actor { return Actor{UserID: h.holder} }

// jobClient builds a real River client against the test database, so the
// transactional-enqueue guarantee can be checked against the actual job table
// rather than against a fake that agrees with the code by construction.
func (h *harness) jobClient(t *testing.T) *jobs.Client {
	t.Helper()

	settings, err := jobs.NewSettings(1, time.Second)
	if err != nil {
		t.Fatalf("jobs.NewSettings: %v", err)
	}
	registry := jobs.NewRegistry()
	// A worker per kind, because River refuses a client whose queue can hold
	// a job it has no worker for — and a queue that silently accepts one is a
	// job nobody will ever run.
	jobs.Register(registry, river.WorkFunc(func(context.Context, *river.Job[ConfirmationArgs]) error { return nil }))
	jobs.Register(registry, river.WorkFunc(func(context.Context, *river.Job[ReminderArgs]) error { return nil }))
	jobs.Register(registry, river.WorkFunc(func(context.Context, *river.Job[NoShowCheckArgs]) error { return nil }))

	client, err := jobs.New(h.pool, registry, settings, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	return client
}

// queuedKinds reads the job table directly. Reading it rather than asking the
// client is the point: the guarantee is about rows, and a client-side count
// would agree with the code that wrote it.
func (h *harness) queuedKinds(t *testing.T) []string {
	t.Helper()

	rows, err := h.pool.Query(h.ctx, `SELECT kind FROM river_job ORDER BY id`)
	if err != nil {
		t.Fatalf("reading the job queue: %v", err)
	}
	defer rows.Close()

	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scanning a job: %v", err)
		}
		kinds = append(kinds, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the job queue: %v", err)
	}
	return kinds
}
