// Generated once by nise; owned by this application. Nise will not overwrite it.

package resource

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	migrationdb "workbench/db"
	"workbench/internal/platform/database"
	"workbench/internal/platform/database/dbtest"
)

const testTimeout = 60 * time.Second

type harness struct {
	ctx        context.Context
	pool       *pgxpool.Pool
	transactor *database.Transactor
	resources  *Resources
	orgID      string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	testDatabase := dbtest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	settings, err := database.NewSettings(6, 0, time.Minute, time.Minute, time.Second, 5*time.Second)
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

	// A role that cannot bypass row-level security. Run as a superuser, every
	// isolation assertion below would pass vacuously.
	databaseURL, databasePassword := testDatabase.Restricted(t)
	pool, err := database.Open(ctx, databaseURL, databasePassword, settings, database.OpenOptions{})
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	isolation, err := database.CheckTenantIsolation(ctx, pool)
	if err != nil {
		t.Fatalf("CheckTenantIsolation: %v", err)
	}
	if !isolation.Enforced() {
		t.Fatalf("the restricted role still bypasses row-level security (%s)", isolation.Reason())
	}

	transactor, err := database.NewTransactor(pool)
	if err != nil {
		t.Fatalf("NewTransactor: %v", err)
	}
	resources, err := New(transactor)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h := &harness{ctx: ctx, pool: pool, transactor: transactor, resources: resources}
	h.orgID = h.newOrg(t, "lab")
	return h
}

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

func (h *harness) create(t *testing.T, name string) Resource {
	t.Helper()

	created, err := h.resources.Create(h.ctx, h.orgID, Details{Name: name})
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	return created
}

func TestCreatingAResourceStoresItsDetails(t *testing.T) {
	h := newHarness(t)

	created, err := h.resources.Create(h.ctx, h.orgID, Details{
		Name: "  Microscope 2  ", Description: " the good one ", Location: " Bench 4 ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Whitespace is trimmed before storage, so two people typing the same
	// name with different spacing do not create two resources.
	if created.Name != "Microscope 2" || created.Description != "the good one" || created.Location != "Bench 4" {
		t.Errorf("stored %+v; the fields were not trimmed", created)
	}
	if created.OrgID != h.orgID {
		t.Errorf("orgID = %q, want the caller's tenant %q", created.OrgID, h.orgID)
	}
	if !created.InService() {
		t.Error("a new resource is not in service")
	}
}

// The tenant is written by the row-level-security policy rather than passed as
// a parameter, so there is no argument a caller can get wrong. This is the
// assertion that the policy is what filled it in.
func TestAResourceIsCreatedInTheCallersTenantOnly(t *testing.T) {
	h := newHarness(t)
	other := h.newOrg(t, "other")

	mine := h.create(t, "Microscope 2")

	page, err := h.resources.List(h.ctx, other, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Resources) != 0 {
		t.Fatalf("another tenant sees %d resource(s)", len(page.Resources))
	}
	if _, err := h.resources.Get(h.ctx, other, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get across tenants: err = %v, want ErrNotFound", err)
	}
}

func TestTwoResourcesInOneOrganizationCannotShareAName(t *testing.T) {
	h := newHarness(t)
	h.create(t, "Microscope 2")

	// Case-insensitively: the index is on lower(name), because "microscope 2"
	// and "Microscope 2" are the same instrument to everybody but a byte
	// comparison.
	_, err := h.resources.Create(h.ctx, h.orgID, Details{Name: "microscope 2"})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

// A different organization may use the name freely. The uniqueness is per
// tenant, and a global one would leak the existence of another tenant's
// equipment through a refusal.
func TestAnotherOrganizationMayReuseAName(t *testing.T) {
	h := newHarness(t)
	h.create(t, "Microscope 2")
	other := h.newOrg(t, "other")

	if _, err := h.resources.Create(h.ctx, other, Details{Name: "Microscope 2"}); err != nil {
		t.Fatalf("another organization could not use the name: %v", err)
	}
}

func TestRetiringIsRecordedAndNotRepeated(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")

	retired, err := h.resources.Retire(h.ctx, h.orgID, created.ID)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if retired.InService() || retired.RetiredAt.IsZero() {
		t.Fatalf("after retiring: retiredAt=%v", retired.RetiredAt)
	}

	// Retiring again is reported rather than silently moving the timestamp.
	// When it happened is the one fact anybody asks about afterwards.
	_, err = h.resources.Retire(h.ctx, h.orgID, created.ID)
	if !errors.Is(err, ErrRetired) {
		t.Fatalf("err = %v, want ErrRetired", err)
	}
	again, err := h.resources.Get(h.ctx, h.orgID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !again.RetiredAt.Equal(retired.RetiredAt) {
		t.Errorf("the second attempt moved retiredAt from %s to %s", retired.RetiredAt, again.RetiredAt)
	}
}

// A retired resource frees its name, so a lab that retires "Microscope 2" and
// buys a replacement can call the new one "Microscope 2".
func TestARetiredResourceFreesItsName(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")

	if _, err := h.resources.Retire(h.ctx, h.orgID, created.ID); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	h.create(t, "Microscope 2")
}

// A retired resource is visible and not editable, and those are two different
// answers. Telling somebody a resource does not exist when they can see it in
// a list is how a person concludes the software is broken.
func TestARetiredResourceIsVisibleButNotEditable(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")
	if _, err := h.resources.Retire(h.ctx, h.orgID, created.ID); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	if _, err := h.resources.Get(h.ctx, h.orgID, created.ID); err != nil {
		t.Errorf("a retired resource is not readable: %v", err)
	}
	_, err := h.resources.Update(h.ctx, h.orgID, created.ID, Details{Name: "Microscope 3"})
	if !errors.Is(err, ErrRetired) {
		t.Errorf("err = %v, want ErrRetired", err)
	}
}

func TestAResourceThatDoesNotExistIsNotFound(t *testing.T) {
	h := newHarness(t)

	for name, id := range map[string]string{
		"a well-formed identifier nothing uses": "44444444-4444-4444-4444-444444444444",
		"not an identifier at all":              "not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.resources.Get(h.ctx, h.orgID, id); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get: err = %v, want ErrNotFound", err)
			}
			if _, err := h.resources.Retire(h.ctx, h.orgID, id); !errors.Is(err, ErrNotFound) {
				t.Errorf("Retire: err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestInvalidDetailsAreRefused(t *testing.T) {
	h := newHarness(t)

	cases := map[string]Details{
		"no name":              {Name: ""},
		"whitespace name":      {Name: "   "},
		"name too long":        {Name: strings.Repeat("n", MaxNameBytes+1)},
		"description too long": {Name: "ok", Description: strings.Repeat("d", MaxDescriptionBytes+1)},
		"location too long":    {Name: "ok", Location: strings.Repeat("l", MaxLocationBytes+1)},
	}
	for name, details := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.resources.Create(h.ctx, h.orgID, details); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestListingExcludesRetiredResourcesUnlessAsked(t *testing.T) {
	h := newHarness(t)
	live := h.create(t, "Centrifuge")
	gone := h.create(t, "Microscope 2")
	if _, err := h.resources.Retire(h.ctx, h.orgID, gone.ID); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	page, err := h.resources.List(h.ctx, h.orgID, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Resources) != 1 || page.Resources[0].ID != live.ID {
		t.Fatalf("the default listing returned %d resource(s), want only the one in service", len(page.Resources))
	}
	if page.Total != 1 {
		t.Errorf("total = %d, want 1 — the count must agree with the filter", page.Total)
	}

	withRetired, err := h.resources.List(h.ctx, h.orgID, ListOptions{IncludeRetired: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(withRetired.Resources) != 2 || withRetired.Total != 2 {
		t.Errorf("asking for retired resources returned %d of 2", len(withRetired.Resources))
	}
}

// Pagination walks every row exactly once. The assertion is on the set, not on
// a count: a cursor that skips one row and repeats another gives the right
// count and the wrong answer.
func TestPaginationVisitsEveryResourceExactlyOnce(t *testing.T) {
	h := newHarness(t)

	const total = 7
	want := map[string]bool{}
	for i := range total {
		// Names deliberately not in creation order, so a cursor that happened
		// to work on insertion order would fail here.
		created := h.create(t, string(rune('g'-i))+" instrument")
		want[created.ID] = true
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate")
		}
		got, err := h.resources.List(h.ctx, h.orgID, ListOptions{PageSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, r := range got.Resources {
			seen[r.ID]++
		}
		if got.NextCursor == "" {
			break
		}
		cursor = got.NextCursor
	}

	if len(seen) != total {
		t.Errorf("saw %d resources across all pages, want %d", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("resource %s appeared %d times", id, count)
		}
		if !want[id] {
			t.Errorf("resource %s was not one of the ones created", id)
		}
	}
}

func TestAMalformedCursorIsRefused(t *testing.T) {
	h := newHarness(t)

	for _, cursor := range []string{"not-base64!!", "AAAA", "Zm9v"} {
		if _, err := h.resources.List(h.ctx, h.orgID, ListOptions{Cursor: cursor}); !errors.Is(err, ErrInvalid) {
			t.Errorf("cursor %q: err = %v, want ErrInvalid", cursor, err)
		}
	}
}

func TestAPhotoKeyIsRecordedAndRemovable(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")

	withPhoto, err := h.resources.SetPhoto(h.ctx, h.orgID, created.ID, "uploads/abc123")
	if err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	if withPhoto.PhotoKey != "uploads/abc123" {
		t.Errorf("photoKey = %q, want the key that was set", withPhoto.PhotoKey)
	}

	without, err := h.resources.SetPhoto(h.ctx, h.orgID, created.ID, "")
	if err != nil {
		t.Fatalf("SetPhoto(\"\"): %v", err)
	}
	if without.PhotoKey != "" {
		t.Errorf("photoKey = %q, want it cleared", without.PhotoKey)
	}
}

// A transaction that established no tenant reads nothing rather than
// everything: a forgotten SET LOCAL produces an empty page, which somebody
// notices, and never another tenant's data, which nobody does.
func TestNoTenantContextSeesNothing(t *testing.T) {
	h := newHarness(t)
	h.create(t, "Microscope 2")

	var count int64
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM resources`).Scan(&count); err != nil {
		t.Fatalf("counting without a tenant: %v", err)
	}
	if count != 0 {
		t.Errorf("a query with no tenant context read %d resource(s), want 0", count)
	}
}
