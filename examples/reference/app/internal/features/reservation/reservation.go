// Generated once by nise; owned by this application. Nise will not overwrite it.

// Package reservation is a booking of a shared resource, and the state
// machine it moves through.
//
//	               cancel
//	   ┌──────────────────────────────┐
//	   │                              ▼
//	booked ──check-out──▶ checked_out ──return──▶ returned
//	   │
//	   └──(window ended, never collected)──▶ no_show
//
// # Why check-out and return are not updates
//
// They look like writes to a state column, and treating them as such is the
// mistake this package is arranged to avoid. Each has preconditions that are
// about the world rather than about the row — the window has started, the
// caller is the holder or a steward — each records who did it, and each is a
// thing that either happened or did not. A `PATCH` that set `state` would let
// a client move a reservation from `booked` straight to `returned`, and there
// is no server-side check that meaningfully rejects a request whose shape says
// it is allowed.
//
// # Where double-booking is prevented
//
// Not here. Two people reserving one resource for overlapping windows is the
// failure this whole domain is about, and it cannot be prevented by reading
// before writing: under concurrency, both transactions read "free" and both
// insert.
//
// The exclusion constraint on the table refuses the second one, because
// PostgreSQL evaluates it at write time against rows the reader could not have
// seen. This package's job is to turn that refusal into a message naming the
// conflict, not to try to get there first.
//
// # Every transition locks its row
//
// A transition reads the current state, decides, and writes the new one. That
// is three steps, and without `FOR UPDATE` two concurrent check-outs both read
// `booked` and the second overwrites the first's timestamp. The update
// statements also carry the expected state in their WHERE clause, so the
// transition stays atomic even if a future caller forgets to lock.
package reservation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	"workbench/internal/features/reservation/store"
	"workbench/internal/platform/database"
)

// State is where a reservation is in its life.
type State string

const (
	// StateBooked is reserved and not yet collected.
	StateBooked State = "booked"
	// StateCheckedOut is in somebody's hands.
	StateCheckedOut State = "checked_out"
	// StateReturned is finished.
	StateReturned State = "returned"
	// StateCancelled was given up before collection.
	StateCancelled State = "cancelled"
	// StateNoShow is a window that closed with the resource never collected.
	// It is distinct from cancelled on purpose: cancelling frees the slot for
	// somebody else in time to use it, and not turning up does not.
	StateNoShow State = "no_show"
)

// Bounds, matching the schema's own constraints so a violation is reported
// here with a readable message rather than by PostgreSQL with a constraint
// name.
const (
	MaxNoteBytes     = 2000
	MaxPhotoKeyBytes = 512

	// MinWindow and MaxWindow bound a booking. The minimum keeps the calendar
	// legible; the maximum stops one person holding a shared instrument for a
	// year through a typo in a date.
	MinWindow = 15 * time.Minute
	MaxWindow = 30 * 24 * time.Hour

	// DefaultPageSize and MaxPageSize bound a listing.
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Errors this package returns.
var (
	// ErrNotFound reports a reservation that does not exist, or one outside
	// this transaction's tenant.
	ErrNotFound = errors.New("reservation: no such reservation")
	// ErrInvalid reports input that does not satisfy the schema.
	ErrInvalid = errors.New("reservation: invalid reservation")
	// ErrConflict reports a window already taken by a live reservation of the
	// same resource. It is the exclusion constraint, translated.
	ErrConflict = errors.New("reservation: that window is already booked")
	// ErrWrongState reports a transition the reservation's current state does
	// not allow. It carries the states so a caller can say which.
	ErrWrongState = errors.New("reservation: the reservation is not in a state that allows this")
	// ErrNotStarted reports checking out before the window opens. Somebody
	// arriving early is a normal thing to do and a normal thing to refuse, so
	// it has its own error rather than sharing ErrWrongState.
	ErrNotStarted = errors.New("reservation: the reservation's window has not started")
	// ErrNotHolder reports acting on somebody else's reservation without the
	// permission to. The permission check itself lives in the caller; this is
	// the value it returns.
	ErrNotHolder = errors.New("reservation: this reservation belongs to somebody else")
	// ErrResourceUnavailable reports booking a resource that does not exist,
	// is retired, or is in another organization. They are one value for the
	// same reason ErrNotFound covers two cases: which one it is, is itself
	// information.
	ErrResourceUnavailable = errors.New("reservation: that resource cannot be booked")
)

// Reservation is one booking.
type Reservation struct {
	ID         string
	OrgID      string
	ResourceID string
	HolderID   string
	StartsAt   time.Time
	EndsAt     time.Time
	State      State
	Note       string
	ReturnNote string
	// ReturnPhotoKey is the storage key of a photo taken at return, empty
	// when there is none.
	ReturnPhotoKey string
	CheckedOutAt   time.Time
	ReturnedAt     time.Time
	CancelledAt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Page is one page of reservations.
type Page struct {
	Reservations []Reservation
	NextCursor   string
	Total        int64
}

// Reservations is the use case.
type Reservations struct {
	transactor *database.Transactor
	// now is the clock. It is a field rather than a call to time.Now so that
	// the check-out precondition and the no-show sweep are testable without
	// waiting, which is what makes them tested at all.
	now func() time.Time
}

// New builds it.
func New(transactor *database.Transactor) (*Reservations, error) {
	if transactor == nil {
		return nil, errors.New("reservation: a transaction runner is required")
	}
	return &Reservations{transactor: transactor, now: time.Now}, nil
}

// WithClock returns a copy that reads the time from now.
//
// Exported because the tests are in a separate package, and because the
// alternative — a package-level variable a test reassigns — makes two parallel
// tests share one clock.
func (r *Reservations) WithClock(now func() time.Time) *Reservations {
	copied := *r
	copied.now = now
	return &copied
}

// Request is a new booking.
type Request struct {
	ResourceID string
	HolderID   string
	StartsAt   time.Time
	EndsAt     time.Time
	Note       string
}

// Create books a resource for a window.
//
// The window's availability is not checked before the insert, and that is the
// design rather than an omission: see this package's doc comment. A conflict
// comes back from PostgreSQL as an exclusion-constraint violation and is
// translated into ErrConflict here.
func (r *Reservations) Create(ctx context.Context, orgID string, request Request) (Reservation, error) {
	resourceID, err := parseUUID(request.ResourceID)
	if err != nil {
		return Reservation{}, ErrResourceUnavailable
	}
	holderID, err := parseUUID(request.HolderID)
	if err != nil {
		return Reservation{}, fmt.Errorf("%w: the holder is not a valid identifier", ErrInvalid)
	}
	if err := validateWindow(request.StartsAt, request.EndsAt); err != nil {
		return Reservation{}, err
	}
	note := strings.TrimSpace(request.Note)
	if utf8.RuneCountInString(note) > MaxNoteBytes {
		return Reservation{}, fmt.Errorf("%w: the note is longer than %d characters", ErrInvalid, MaxNoteBytes)
	}

	var row store.Reservation
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		var txErr error
		row, txErr = store.New(tx).CreateReservation(ctx, store.CreateReservationParams{
			ResourceID: resourceID,
			HolderID:   holderID,
			StartsAt:   timestamptz(request.StartsAt),
			EndsAt:     timestamptz(request.EndsAt),
			Note:       note,
		})
		return txErr
	})
	if err != nil {
		switch {
		case isOverlap(err):
			return Reservation{}, ErrConflict
		case isUnbookableResource(err):
			// The composite foreign key refused: the resource does not exist,
			// or belongs to another organization. Row-level security has
			// already hidden the second case from every read, so this is the
			// write path saying the same thing.
			return Reservation{}, ErrResourceUnavailable
		}
		return Reservation{}, fmt.Errorf("reservation: creating: %w", err)
	}
	return toReservation(row), nil
}

// Get returns one reservation.
func (r *Reservations) Get(ctx context.Context, orgID, reservationID string) (Reservation, error) {
	id, err := parseUUID(reservationID)
	if err != nil {
		return Reservation{}, ErrNotFound
	}
	var row store.Reservation
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{Access: transaction.ReadOnly},
		func(ctx context.Context, tx pgx.Tx) error {
			var txErr error
			row, txErr = store.New(tx).GetReservation(ctx, store.GetReservationParams{ID: id})
			return txErr
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, ErrNotFound
		}
		return Reservation{}, fmt.Errorf("reservation: reading: %w", err)
	}
	return toReservation(row), nil
}

// Actor is who is asking, and what they are allowed to do to other people's
// reservations.
//
// It is a value rather than two arguments because the pair is meaningless
// apart: "user X" and "may manage" only decide anything together, and a
// signature taking them separately is one somebody eventually calls with the
// wrong boolean.
type Actor struct {
	// UserID is the account making the request.
	UserID string
	// MayManageOthers is true when the account holds reservations.manage.
	// The permission check happens in the caller, against the authorization
	// catalog; this is its result.
	MayManageOthers bool
}

// mayAct reports whether the actor may act on a reservation.
//
// The asymmetry here is the whole reason the `reservations.manage` permission
// exists: acting on your own reservation needs no permission at all, because
// it is yours. A plain permission check would have made every member a
// steward or no member able to return their own equipment.
func (a Actor) mayAct(holderID string) bool {
	return a.MayManageOthers || a.UserID == holderID
}

// CheckOut is the first domain command: the resource has been collected.
//
// It refuses a window that has not started. Somebody arriving early is normal,
// and letting them check out early would make the calendar lie about when the
// instrument is actually in use — which is the question the calendar exists to
// answer.
func (r *Reservations) CheckOut(ctx context.Context, orgID, reservationID string, actor Actor) (Reservation, error) {
	return r.transition(ctx, orgID, reservationID, actor, StateBooked,
		func(ctx context.Context, queries *store.Queries, current store.Reservation) (store.Reservation, error) {
			if r.now().Before(rangeLower(current.During)) {
				return store.Reservation{}, ErrNotStarted
			}
			return queries.CheckOutReservation(ctx, store.CheckOutReservationParams{ID: current.ID})
		})
}

// Return is the second domain command: the resource is back.
//
// It records what the holder said about its condition, and optionally a photo,
// because the state after a return is the thing the next person inherits and
// "it was already like that" is the argument this field exists to settle.
func (r *Reservations) Return(ctx context.Context, orgID, reservationID string, actor Actor, note, photoKey string) (Reservation, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxNoteBytes {
		return Reservation{}, fmt.Errorf("%w: the return note is longer than %d characters", ErrInvalid, MaxNoteBytes)
	}
	if utf8.RuneCountInString(photoKey) > MaxPhotoKeyBytes {
		return Reservation{}, fmt.Errorf("%w: the photo key is longer than %d characters", ErrInvalid, MaxPhotoKeyBytes)
	}

	return r.transition(ctx, orgID, reservationID, actor, StateCheckedOut,
		func(ctx context.Context, queries *store.Queries, current store.Reservation) (store.Reservation, error) {
			return queries.ReturnReservation(ctx, store.ReturnReservationParams{
				ID:             current.ID,
				ReturnNote:     note,
				ReturnPhotoKey: pgtype.Text{String: photoKey, Valid: photoKey != ""},
			})
		})
}

// Cancel gives up a booking that has not been collected.
//
// A checked-out reservation cannot be cancelled — the resource is in somebody's
// hands, and the honest end for that is a return. Cancelling would free the
// slot in the calendar while the instrument was still gone.
func (r *Reservations) Cancel(ctx context.Context, orgID, reservationID string, actor Actor) (Reservation, error) {
	return r.transition(ctx, orgID, reservationID, actor, StateBooked,
		func(ctx context.Context, queries *store.Queries, current store.Reservation) (store.Reservation, error) {
			return queries.CancelReservation(ctx, store.CancelReservationParams{ID: current.ID})
		})
}

// transition is the shape every domain command shares: lock the row, check
// who is asking, check the state, then act.
//
// Writing it once rather than three times is not only tidiness. The lock and
// the state check are the two steps that are easy to omit, and a command that
// omitted either would pass every test that did not run two of it at once.
func (r *Reservations) transition(
	ctx context.Context,
	orgID, reservationID string,
	actor Actor,
	required State,
	act func(context.Context, *store.Queries, store.Reservation) (store.Reservation, error),
) (Reservation, error) {
	id, err := parseUUID(reservationID)
	if err != nil {
		return Reservation{}, ErrNotFound
	}

	var row store.Reservation
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		queries := store.New(tx)

		current, txErr := queries.GetReservationForUpdate(ctx, store.GetReservationForUpdateParams{ID: id})
		if errors.Is(txErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if txErr != nil {
			return txErr
		}

		if !actor.mayAct(uuidString(current.HolderID)) {
			return ErrNotHolder
		}
		if State(current.State) != required {
			return fmt.Errorf("%w: it is %s and this needs it to be %s", ErrWrongState, current.State, required)
		}

		updated, actErr := act(ctx, queries, current)
		if errors.Is(actErr, pgx.ErrNoRows) {
			// The update's own WHERE clause refused. Reaching here means the
			// row changed state between the locking read and the write, which
			// the lock is supposed to prevent — so it is defence in depth
			// firing, not a normal path. It is still a refusal a person asked
			// for and must read as one: "no rows in result set" tells them
			// nothing they can act on.
			return fmt.Errorf("%w: it changed while this request was being handled", ErrWrongState)
		}
		if actErr != nil {
			return actErr
		}
		row = updated
		return nil
	})
	if err != nil {
		for _, known := range []error{ErrNotFound, ErrNotHolder, ErrWrongState, ErrNotStarted} {
			if errors.Is(err, known) {
				return Reservation{}, err
			}
		}
		return Reservation{}, fmt.Errorf("reservation: transition: %w", err)
	}
	return toReservation(row), nil
}

// SweepNoShows marks every booked reservation whose window has closed.
//
// The cutoff is a parameter rather than the clock, so a test can run it
// without waiting and an operator can replay it for a past window after an
// outage without it seeing a "now" different from the one being repaired.
//
// It returns what it changed, because the caller enqueues a notification for
// each and a count would not tell it whom to notify.
func (r *Reservations) SweepNoShows(ctx context.Context, orgID string, cutoff time.Time) ([]Reservation, error) {
	var rows []store.Reservation
	err := r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		var txErr error
		rows, txErr = store.New(tx).MarkNoShows(ctx, store.MarkNoShowsParams{Cutoff: timestamptz(cutoff)})
		return txErr
	})
	if err != nil {
		return nil, fmt.Errorf("reservation: sweeping no-shows: %w", err)
	}
	swept := make([]Reservation, 0, len(rows))
	for _, row := range rows {
		swept = append(swept, toReservation(row))
	}
	return swept, nil
}

// ListOptions filter and page a listing. Every filter is optional, and the
// zero value lists everything the tenant can see.
type ListOptions struct {
	// ResourceID limits to one resource: the calendar.
	ResourceID string
	// HolderID limits to one person: "my reservations".
	HolderID string
	// States limits to particular states. Empty means every state.
	States []State
	// From and To limit to reservations overlapping a window. Both must be
	// set together, or neither.
	From, To time.Time
	// Cursor is the NextCursor of a previous page.
	Cursor string
	// PageSize is bounded by MaxPageSize; zero means DefaultPageSize.
	PageSize int
}

// List returns one page of reservations, ordered by window start.
func (r *Reservations) List(ctx context.Context, orgID string, opts ListOptions) (Page, error) {
	params, size, err := opts.params()
	if err != nil {
		return Page{}, err
	}

	var (
		rows  []store.Reservation
		total int64
	)
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{Access: transaction.ReadOnly},
		func(ctx context.Context, tx pgx.Tx) error {
			queries := store.New(tx)
			listed, txErr := queries.ListReservations(ctx, params)
			if txErr != nil {
				return txErr
			}
			rows = listed
			total, txErr = queries.CountReservations(ctx, store.CountReservationsParams{
				ResourceID:  params.ResourceID,
				HolderID:    params.HolderID,
				States:      params.States,
				WindowStart: params.WindowStart,
				WindowEnd:   params.WindowEnd,
			})
			return txErr
		})
	if err != nil {
		return Page{}, fmt.Errorf("reservation: listing: %w", err)
	}

	page := Page{Total: total, Reservations: make([]Reservation, 0, size)}
	for i, row := range rows {
		if i == size {
			last := rows[size-1]
			page.NextCursor = encodeCursor(rangeLower(last.During), uuidString(last.ID))
			break
		}
		page.Reservations = append(page.Reservations, toReservation(row))
	}
	return page, nil
}

// validateWindow bounds a booking, matching the schema's own constraints.
func validateWindow(startsAt, endsAt time.Time) error {
	if startsAt.IsZero() || endsAt.IsZero() {
		return fmt.Errorf("%w: a reservation needs a start and an end", ErrInvalid)
	}
	if !endsAt.After(startsAt) {
		return fmt.Errorf("%w: the reservation ends before it starts", ErrInvalid)
	}
	length := endsAt.Sub(startsAt)
	if length < MinWindow {
		return fmt.Errorf("%w: a reservation must be at least %s long", ErrInvalid, MinWindow)
	}
	if length > MaxWindow {
		return fmt.Errorf("%w: a reservation may be at most %s long", ErrInvalid, MaxWindow)
	}
	return nil
}
