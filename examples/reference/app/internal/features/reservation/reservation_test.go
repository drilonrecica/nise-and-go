// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	"workbench/internal/features/reservation/store"
)

// --- Booking, and the constraint the whole domain is about ----------------

func TestBookingAResourceRecordsTheWindow(t *testing.T) {
	h := newHarness(t)

	booked := h.book(t, 1, 3)
	if booked.State != StateBooked {
		t.Errorf("state = %q, want %q", booked.State, StateBooked)
	}
	if !booked.StartsAt.Equal(testClock.Add(time.Hour)) || !booked.EndsAt.Equal(testClock.Add(3*time.Hour)) {
		t.Errorf("window = %s..%s, want %s..%s",
			booked.StartsAt, booked.EndsAt, testClock.Add(time.Hour), testClock.Add(3*time.Hour))
	}
	if booked.HolderID != h.holder {
		t.Errorf("holder = %q, want %q", booked.HolderID, h.holder)
	}
	// A booking has no completion timestamps yet, and the schema's own
	// constraints say so — this asserts the mapping did not invent one.
	if !booked.CheckedOutAt.IsZero() || !booked.ReturnedAt.IsZero() || !booked.CancelledAt.IsZero() {
		t.Errorf("a fresh booking carries a completion timestamp: %+v", booked)
	}
}

func TestAnOverlappingBookingIsRefused(t *testing.T) {
	h := newHarness(t)
	h.book(t, 1, 4)

	_, err := h.reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID,
		HolderID:   h.holder,
		StartsAt:   testClock.Add(3 * time.Hour),
		EndsAt:     testClock.Add(5 * time.Hour),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// Half-open windows are what make a shared calendar behave the way anybody
// sharing equipment expects: a booking ending at 10:00 and one starting at
// 10:00 do not overlap. A closed range gets this wrong, and gets it wrong in
// the direction that makes the software look broken.
func TestBackToBackBookingsAreAllowed(t *testing.T) {
	h := newHarness(t)
	h.book(t, 1, 3)
	h.book(t, 3, 5)
}

// The test this whole design exists for.
//
// Two transactions both read "the window is free" — which is true when they
// read it — and both insert. A check written in Go passes for both. Exactly
// one may commit, and the one that does not must be told why.
func TestTwoConcurrentBookingsOfOneWindowLeaveExactlyOne(t *testing.T) {
	// A two-connection pool, so the two transactions genuinely run at once.
	h := newHarnessWithPoolSize(t, 4)

	const attempts = 2
	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)
	start := make(chan struct{})

	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := h.reservations.Create(h.ctx, h.orgID, Request{
				ResourceID: h.resource.ID,
				HolderID:   h.holder,
				StartsAt:   testClock.Add(24 * time.Hour),
				EndsAt:     testClock.Add(26 * time.Hour),
			})
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wait.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if succeeded != 1 || conflicts != attempts-1 {
		t.Fatalf("%d booking(s) succeeded and %d conflicted; exactly one must succeed", succeeded, conflicts)
	}

	// And the database agrees, which is the assertion that would catch a
	// count kept only in this test's own variables.
	page, err := h.reservations.List(h.ctx, h.orgID, ListOptions{States: []State{StateBooked}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("the database holds %d live reservations for that window, want 1", page.Total)
	}
}

// A cancelled reservation must not keep its slot: the exclusion constraint's
// WHERE clause is what allows the slot to be rebooked, and dropping it would
// make history block the future.
func TestACancelledBookingFreesItsWindow(t *testing.T) {
	h := newHarness(t)
	first := h.book(t, 1, 3)

	if _, err := h.reservations.Cancel(h.ctx, h.orgID, first.ID, h.holderActor()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.book(t, 1, 3)
}

func TestAnImplausibleWindowIsRefused(t *testing.T) {
	h := newHarness(t)

	cases := map[string]struct{ from, to time.Duration }{
		"ends before it starts": {3 * time.Hour, time.Hour},
		"zero length":           {time.Hour, time.Hour},
		"under the minimum":     {time.Hour, time.Hour + 5*time.Minute},
		"over the maximum":      {time.Hour, MaxWindow + 2*time.Hour},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.reservations.Create(h.ctx, h.orgID, Request{
				ResourceID: h.resource.ID,
				HolderID:   h.holder,
				StartsAt:   testClock.Add(c.from),
				EndsAt:     testClock.Add(c.to),
			})
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestBookingAResourceThatDoesNotExistIsRefused(t *testing.T) {
	h := newHarness(t)

	_, err := h.reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: "44444444-4444-4444-4444-444444444444",
		HolderID:   h.holder,
		StartsAt:   testClock.Add(time.Hour),
		EndsAt:     testClock.Add(3 * time.Hour),
	})
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("err = %v, want ErrResourceUnavailable", err)
	}
}

// --- The domain commands --------------------------------------------------

func TestCheckOutAndReturnMoveThroughTheStates(t *testing.T) {
	h := newHarness(t)
	// A window that has already opened, so check-out is allowed.
	booked := h.book(t, -1, 2)

	checkedOut, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor())
	if err != nil {
		t.Fatalf("CheckOut: %v", err)
	}
	if checkedOut.State != StateCheckedOut || checkedOut.CheckedOutAt.IsZero() {
		t.Fatalf("after check-out: state=%q checkedOutAt=%v", checkedOut.State, checkedOut.CheckedOutAt)
	}

	returned, err := h.reservations.Return(h.ctx, h.orgID, booked.ID, h.holderActor(), "scratched the stage", "")
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if returned.State != StateReturned || returned.ReturnedAt.IsZero() {
		t.Fatalf("after return: state=%q returnedAt=%v", returned.State, returned.ReturnedAt)
	}
	if returned.ReturnNote != "scratched the stage" {
		t.Errorf("return note = %q, want the note that was given", returned.ReturnNote)
	}
	// The check-out timestamp survives the return: "when was it taken" is a
	// question somebody asks afterwards.
	if returned.CheckedOutAt.IsZero() {
		t.Error("the return cleared the check-out timestamp")
	}
}

// Somebody arriving early is normal, and so is refusing them. Letting a
// reservation be checked out before its window would make the calendar lie
// about when the instrument is actually in use, which is the one question the
// calendar exists to answer.
func TestCheckingOutBeforeTheWindowOpensIsRefused(t *testing.T) {
	h := newHarness(t)
	booked := h.book(t, 2, 4)

	_, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor())
	if !errors.Is(err, ErrNotStarted) {
		t.Fatalf("err = %v, want ErrNotStarted", err)
	}
}

// The state machine has no shortcuts. Each of these is a transition somebody
// would reach for, and each has to fail with a message naming both states.
func TestEveryIllegalTransitionIsRefused(t *testing.T) {
	h := newHarness(t)

	t.Run("returning something never checked out", func(t *testing.T) {
		booked := h.book(t, 100, 102)
		_, err := h.reservations.Return(h.ctx, h.orgID, booked.ID, h.holderActor(), "", "")
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("err = %v, want ErrWrongState", err)
		}
		if !strings.Contains(err.Error(), "booked") || !strings.Contains(err.Error(), "checked_out") {
			t.Errorf("the refusal does not name both states: %v", err)
		}
	})

	t.Run("checking out twice", func(t *testing.T) {
		booked := h.book(t, -3, -1)
		if _, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
			t.Fatalf("CheckOut: %v", err)
		}
		_, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor())
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("err = %v, want ErrWrongState", err)
		}
	})

	t.Run("cancelling something already collected", func(t *testing.T) {
		booked := h.book(t, -6, -4)
		if _, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
			t.Fatalf("CheckOut: %v", err)
		}
		// The resource is in somebody's hands; the honest end for that is a
		// return, and cancelling would free the slot while it was still gone.
		_, err := h.reservations.Cancel(h.ctx, h.orgID, booked.ID, h.holderActor())
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("err = %v, want ErrWrongState", err)
		}
	})

	t.Run("returning twice", func(t *testing.T) {
		booked := h.book(t, -9, -7)
		if _, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
			t.Fatalf("CheckOut: %v", err)
		}
		if _, err := h.reservations.Return(h.ctx, h.orgID, booked.ID, h.holderActor(), "", ""); err != nil {
			t.Fatalf("Return: %v", err)
		}
		_, err := h.reservations.Return(h.ctx, h.orgID, booked.ID, h.holderActor(), "", "")
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("err = %v, want ErrWrongState", err)
		}
	})
}

// The asymmetry the reservations.manage permission exists for: acting on your
// own reservation needs no permission, and acting on somebody else's needs
// exactly this one.
func TestActingOnSomebodyElsesReservationNeedsThePermission(t *testing.T) {
	h := newHarness(t)
	booked := h.book(t, -1, 2)
	stranger := h.addUser(t, "stranger@example.com")

	_, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, Actor{UserID: stranger})
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("err = %v, want ErrNotHolder", err)
	}

	// The same person, holding reservations.manage.
	checkedOut, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID,
		Actor{UserID: stranger, MayManageOthers: true})
	if err != nil {
		t.Fatalf("a steward could not check out somebody else's reservation: %v", err)
	}
	if checkedOut.State != StateCheckedOut {
		t.Errorf("state = %q, want %q", checkedOut.State, StateCheckedOut)
	}
	// And the reservation still belongs to the person who booked it. A
	// steward acting on it is not a transfer of ownership.
	if checkedOut.HolderID != h.holder {
		t.Errorf("holder = %q, want the original holder %q", checkedOut.HolderID, h.holder)
	}
}

// Two concurrent check-outs of one reservation: exactly one may succeed.
//
// This is a smoke test, and it is worth saying which claim it does *not*
// support. Sabotaging either mechanism — removing FOR UPDATE, or removing the
// state guard from the UPDATE — leaves it passing, because the two goroutines
// usually do not interleave badly enough to need either. The two mechanisms
// are proved individually by the two tests after it, and this one stays
// because "exactly one wins" is still the property a reader of the domain
// cares about.
func TestTwoConcurrentCheckOutsLeaveExactlyOne(t *testing.T) {
	h := newHarnessWithPoolSize(t, 4)
	booked := h.book(t, -1, 2)

	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
		refused   int
		other     []error
	)
	start := make(chan struct{})
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := h.reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor())
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrWrongState):
				refused++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wait.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("%d check-outs succeeded and %d were refused; exactly one must succeed", succeeded, refused)
	}
}

// --- The sweep ------------------------------------------------------------

func TestTheSweepMarksOnlyWindowsThatClosedUncollected(t *testing.T) {
	h := newHarness(t)

	missed := h.book(t, -5, -3)    // window closed, never collected
	collected := h.book(t, -2, -1) // window closed, but checked out
	upcoming := h.book(t, 5, 7)    // window has not closed
	if _, err := h.reservations.CheckOut(h.ctx, h.orgID, collected.ID, h.holderActor()); err != nil {
		t.Fatalf("CheckOut: %v", err)
	}

	swept, err := h.reservations.SweepNoShows(h.ctx, h.orgID, testClock)
	if err != nil {
		t.Fatalf("SweepNoShows: %v", err)
	}
	if len(swept) != 1 || swept[0].ID != missed.ID {
		t.Fatalf("the sweep marked %d reservation(s) %v, want only the uncollected one", len(swept), swept)
	}
	if swept[0].State != StateNoShow {
		t.Errorf("state = %q, want %q", swept[0].State, StateNoShow)
	}

	// The other two are untouched. A sweep that also caught the checked-out
	// one would mark somebody a no-show while they were holding the
	// instrument.
	for _, untouched := range []Reservation{collected, upcoming} {
		got, getErr := h.reservations.Get(h.ctx, h.orgID, untouched.ID)
		if getErr != nil {
			t.Fatalf("Get: %v", getErr)
		}
		if got.State == StateNoShow {
			t.Errorf("the sweep marked %s as a no-show", got.ID)
		}
	}
}

// A no-show frees its slot, like a cancellation: the window is gone, and
// blocking the calendar with it helps nobody.
func TestASweptReservationFreesItsWindow(t *testing.T) {
	h := newHarness(t)
	h.book(t, -5, -3)

	if _, err := h.reservations.SweepNoShows(h.ctx, h.orgID, testClock); err != nil {
		t.Fatalf("SweepNoShows: %v", err)
	}
	h.book(t, -5, -3)
}

// --- Filtering and pagination ---------------------------------------------

func TestListingFiltersByResourceHolderStateAndWindow(t *testing.T) {
	h := newHarness(t)

	other := h.newResource(t, h.orgID, "Centrifuge")
	stranger := h.addUser(t, "other-holder@example.com")

	mine := h.book(t, 1, 3)
	theirs, err := h.reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: other.ID, HolderID: stranger,
		StartsAt: testClock.Add(4 * time.Hour), EndsAt: testClock.Add(6 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cancelled := h.book(t, 10, 12)
	if _, err := h.reservations.Cancel(h.ctx, h.orgID, cancelled.ID, h.holderActor()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	cases := map[string]struct {
		opts ListOptions
		want []string
	}{
		"everything":     {ListOptions{}, []string{mine.ID, theirs.ID, cancelled.ID}},
		"by resource":    {ListOptions{ResourceID: other.ID}, []string{theirs.ID}},
		"by holder":      {ListOptions{HolderID: stranger}, []string{theirs.ID}},
		"by state":       {ListOptions{States: []State{StateCancelled}}, []string{cancelled.ID}},
		"by window":      {ListOptions{From: testClock, To: testClock.Add(4 * time.Hour)}, []string{mine.ID}},
		"nothing at all": {ListOptions{ResourceID: other.ID, HolderID: h.holder}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			page, listErr := h.reservations.List(h.ctx, h.orgID, c.opts)
			if listErr != nil {
				t.Fatalf("List: %v", listErr)
			}
			got := make([]string, 0, len(page.Reservations))
			for _, r := range page.Reservations {
				got = append(got, r.ID)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
			if page.Total != int64(len(c.want)) {
				t.Errorf("total = %d, want %d", page.Total, len(c.want))
			}
		})
	}
}

// A half-set window filter is refused rather than silently matching nothing.
// A range ending at NULL overlaps nothing, so the query would return an empty
// page that reads as "no reservations" instead of "your filter was wrong".
func TestAHalfSetWindowFilterIsRefused(t *testing.T) {
	h := newHarness(t)

	_, err := h.reservations.List(h.ctx, h.orgID, ListOptions{From: testClock})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// Pagination walks every row exactly once. The assertion is on the *set*, not
// on a count: a cursor that skips one row and repeats another gives the right
// count and the wrong answer.
func TestPaginationVisitsEveryReservationExactlyOnce(t *testing.T) {
	h := newHarness(t)

	const total = 7
	want := make(map[string]bool, total)
	for i := range total {
		booked := h.book(t, 2*i+1, 2*i+2)
		want[booked.ID] = true
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate")
		}
		got, err := h.reservations.List(h.ctx, h.orgID, ListOptions{PageSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, r := range got.Reservations {
			seen[r.ID]++
		}
		if got.NextCursor == "" {
			break
		}
		cursor = got.NextCursor
	}

	if len(seen) != total {
		t.Errorf("saw %d reservations across all pages, want %d", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("reservation %s appeared %d times", id, count)
		}
		if !want[id] {
			t.Errorf("reservation %s was not one of the ones created", id)
		}
	}
}

func TestAMalformedCursorIsRefused(t *testing.T) {
	h := newHarness(t)

	for _, cursor := range []string{"not-base64!!", "AAAA", "Zm9vX2Jhcg"} {
		if _, err := h.reservations.List(h.ctx, h.orgID, ListOptions{Cursor: cursor}); !errors.Is(err, ErrInvalid) {
			t.Errorf("cursor %q: err = %v, want ErrInvalid", cursor, err)
		}
	}
}

// --- Tenant isolation -----------------------------------------------------

// Row-level security, from this feature's own tables. The organizations module
// proves the mechanism; this proves it was applied to the tables Workbench
// added, which is the part a new feature can forget.
func TestAnotherTenantSeesNothing(t *testing.T) {
	h := newHarness(t)
	mine := h.book(t, 1, 3)

	otherOrg := h.newOrg(t, "other")

	page, err := h.reservations.List(h.ctx, otherOrg, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Reservations) != 0 || page.Total != 0 {
		t.Fatalf("another tenant sees %d reservation(s)", page.Total)
	}

	// And cannot reach one by identifier, which is the path a list filter
	// cannot protect.
	if _, err := h.reservations.Get(h.ctx, otherOrg, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	// Nor act on one.
	if _, err := h.reservations.Cancel(h.ctx, otherOrg, mine.ID, Actor{MayManageOthers: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel across tenants: err = %v, want ErrNotFound", err)
	}
}

// A transaction that established no tenant reads nothing, rather than
// everything. That direction was chosen: a forgotten SET LOCAL produces an
// empty page, which somebody notices, and never another tenant's data.
func TestNoTenantContextSeesNothing(t *testing.T) {
	h := newHarness(t)
	h.book(t, 1, 3)

	var count int64
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM reservations`).Scan(&count); err != nil {
		t.Fatalf("counting without a tenant: %v", err)
	}
	if count != 0 {
		t.Errorf("a query with no tenant context read %d reservation(s), want 0", count)
	}
}

// The locking read, proved directly rather than through an outcome.
//
// A transition is read-decide-write, and without FOR UPDATE two of them can
// both read `booked`. Asserting that through a race is unreliable — the
// goroutines usually serialize on their own, and the test then passes with the
// lock removed. So this asserts the lock itself: while one transaction holds
// the row, a second read of it must not complete.
func TestTheTransitionReadLocksTheRow(t *testing.T) {
	h := newHarnessWithPoolSize(t, 4)
	booked := h.book(t, -1, 2)
	// Resolved on the test goroutine: a t.Fatalf inside one of the goroutines
	// below would Goexit *it*, leaving the other blocked.
	bookedID := mustUUID(t, booked.ID)

	held := make(chan struct{})
	release := make(chan struct{})
	// Released on every path out of this test, not only the happy one.
	//
	// t.Fatalf calls runtime.Goexit, which runs deferred functions and does
	// not run the rest of the test — so a failure between here and the
	// explicit release below would leave the holding goroutine blocked on a
	// channel forever, with an open transaction and a pool connection, and the
	// package would hang until the test binary's timeout instead of failing.
	// That is exactly what a sabotage of the lock produced before this defer
	// existed: a panic after ninety seconds rather than a named failure.
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	done := make(chan error, 1)

	// A holds the row.
	go func() {
		done <- h.transactor.WithinTenant(h.ctx, h.orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := store.New(tx).GetReservationForUpdate(ctx,
				store.GetReservationForUpdateParams{ID: bookedID}); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()

	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the first transaction never acquired the row")
	}

	// B tries the same read. With FOR UPDATE it blocks; without it, it
	// returns immediately, which is what this test exists to notice.
	second := make(chan error, 1)
	go func() {
		second <- h.transactor.WithinTenant(h.ctx, h.orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := store.New(tx).GetReservationForUpdate(ctx,
				store.GetReservationForUpdateParams{ID: bookedID})
			return err
		})
	}()

	select {
	case err := <-second:
		t.Fatalf("a second read of a locked row completed immediately (err=%v); the transition read does not lock", err)
	case <-time.After(750 * time.Millisecond):
		// Still blocked, which is the assertion.
	}

	releaseOnce()
	if err := <-done; err != nil {
		t.Fatalf("the holding transaction failed: %v", err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the second read failed after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second read never completed after the lock was released")
	}
}

// The state guard in each UPDATE's WHERE clause, proved without a race.
//
// It is defence in depth: with the locking read in place, a transition cannot
// normally reach an update whose expected state no longer holds. This drives
// the store directly to reach that state anyway, because a guard that is only
// exercised by a race nobody can reproduce is a guard nobody knows works.
func TestTheUpdateRefusesARowInTheWrongState(t *testing.T) {
	h := newHarness(t)
	booked := h.book(t, -1, 2)

	// Move it out of 'booked' first.
	if _, err := h.reservations.Cancel(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	err := h.transactor.WithinTenant(h.ctx, h.orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		_, updateErr := store.New(tx).CheckOutReservation(ctx,
			store.CheckOutReservationParams{ID: mustUUID(t, booked.ID)})
		return updateErr
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows — the update matched a cancelled reservation", err)
	}

	// And the use case turns that into a refusal a person can read, rather
	// than into "no rows in result set".
	h2 := newHarness(t)
	other := h2.book(t, -1, 2)
	if _, err := h2.reservations.Cancel(h2.ctx, h2.orgID, other.ID, h2.holderActor()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := h2.reservations.CheckOut(h2.ctx, h2.orgID, other.ID, h2.holderActor()); !errors.Is(err, ErrWrongState) {
		t.Errorf("err = %v, want ErrWrongState", err)
	}
}

func mustUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	id, err := parseUUID(value)
	if err != nil {
		t.Fatalf("parseUUID(%q): %v", value, err)
	}
	return id
}
