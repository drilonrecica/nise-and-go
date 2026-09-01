// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"workbench/internal/features/audit"
	"workbench/internal/features/notifications"
)

// The collaborators a reservation change has effects through.
//
// They are interfaces declared here rather than the concrete types, for the
// ordinary Go reason — the consumer defines what it needs — and for one
// specific to this application: each is optional. A `web` process that never
// runs a worker still creates reservations, and a test of the state machine
// should not have to construct a job client to check that returning twice is
// refused.
//
// Every one of them is called **inside the transaction that made the change**.
// That is the property the whole arrangement exists for: an audit record, a
// notification, and an enqueued job become visible if and only if the business
// change committed. A rollback takes all three with it, and there is no
// compensating path to get wrong.
type (
	// AuditRecorder writes an audit record inside a transaction.
	AuditRecorder interface {
		RecordWithin(ctx context.Context, tx pgx.Tx, event audit.Event) (audit.Record, error)
	}
	// Enqueuer inserts a job inside a transaction.
	Enqueuer interface {
		InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error
	}
	// Notifier writes a persistent notification inside a transaction.
	Notifier interface {
		Notify(ctx context.Context, tx pgx.Tx, content notifications.New) (notifications.Notification, error)
	}
)

// Options are the optional collaborators.
type Options struct {
	Audit  AuditRecorder
	Jobs   Enqueuer
	Notify Notifier
}

// The audit actions this feature records. They are constants because an audit
// action is a name in a stored record and a filter somebody types, so a typo
// in one call site would produce a record nobody can find again.
const (
	ActionCreated    = "reservation.created"
	ActionCheckedOut = "reservation.checked_out"
	ActionReturned   = "reservation.returned"
	ActionCancelled  = "reservation.cancelled"
	ActionNoShow     = "reservation.no_show"
)

// subjectKind names what an audit record is about.
const subjectKind = "reservation"

// NotificationKind values a frontend switches on to choose an icon and a link.
// They are the application's own names and never a Go type path: renaming a
// type must not change what is already stored.
const (
	NotifyNoShow     = "reservation.no_show"
	NotifyStartsSoon = "reservation.starts_soon"
)

// ConfirmationArgs asks a worker to send a booking confirmation.
//
// It carries identifiers rather than the rendered message. A job payload is a
// row anybody who can read the database can read (threat model, residual
// risks), so it holds the least that lets the worker do its job — and the
// worker re-reads the reservation, which also means a confirmation sent after
// a retry describes the reservation as it is rather than as it was.
type ConfirmationArgs struct {
	OrgID         string `json:"orgId"`
	ReservationID string `json:"reservationId"`
}

// Kind is the stable name River stores. It is written out rather than derived
// from the Go type, so renaming the type does not orphan queued jobs.
func (ConfirmationArgs) Kind() string { return "reservation_confirmation" }

// ReminderArgs asks a worker to tell a holder their window is about to open.
type ReminderArgs struct {
	OrgID         string `json:"orgId"`
	ReservationID string `json:"reservationId"`
}

func (ReminderArgs) Kind() string { return "reservation_reminder" }

// NoShowCheckArgs asks a worker to close out one reservation whose window has
// ended, if it was never collected.
//
// It is scheduled for the moment the window closes rather than found by a
// periodic sweep, and that is not only an optimization — it is what the
// framework's own tenancy design requires.
//
// A periodic sweep over tenant-scoped data has to enumerate tenants, and there
// is no way to do that: `organizations` is itself tenant-scoped, so a
// transaction that established no tenant reads no organizations. That is the
// row-level-security design working exactly as intended (a forgotten tenant
// reads nothing rather than everything), and a sweep written against it would
// return zero rows and look like a clean run forever.
//
// Driving it per reservation removes the question. The job carries the tenant
// it belongs to, was enqueued in the same transaction as the booking, and runs
// at the one moment it can do anything useful. A cancelled or collected
// reservation is simply not in the state the job acts on.
//
// See the manual path, Reservations.SweepNoShows, for the recovery case: a
// deployment that was down when a window closed, where an operator runs the
// sweep for one organization with a cutoff they choose.
type NoShowCheckArgs struct {
	OrgID         string `json:"orgId"`
	ReservationID string `json:"reservationId"`
}

func (NoShowCheckArgs) Kind() string { return "reservation_no_show_check" }

// ReminderLeadTime is how long before a window opens the reminder is sent.
//
// Scheduled at booking time rather than found by a sweep, because River can
// hold a job until a named instant and a sweep that polls for "starting within
// the hour" either runs often enough to be wasteful or misses the window.
const ReminderLeadTime = time.Hour

// recordAndEnqueue runs the effects of a state change inside its transaction.
//
// It takes the transaction rather than opening one. Every caller is already
// inside the transaction that made the change, and that is the entire point:
// see the collaborators' doc comment above.
func (r *Reservations) recordAndEnqueue(
	ctx context.Context, tx pgx.Tx, action string, actorID string, row Reservation,
) error {
	if r.options.Audit != nil {
		event := audit.Event{
			Action:      action,
			Outcome:     audit.Succeeded,
			ActorKind:   audit.ActorUser,
			ActorID:     actorID,
			SubjectKind: subjectKind,
			SubjectID:   row.ID,
			Detail: map[string]string{
				"resource_id": row.ResourceID,
				"state":       string(row.State),
			},
		}
		if actorID == "" {
			// The sweep has no person behind it. ActorID is required for a
			// user actor and forbidden otherwise, so the kind changes too —
			// recording the system as a user would put an empty identifier in
			// a column somebody later joins on.
			event.ActorKind = audit.ActorSystem
		}
		if _, err := r.options.Audit.RecordWithin(ctx, tx, event); err != nil {
			return fmt.Errorf("recording %s: %w", action, err)
		}
	}
	return nil
}

// scheduleConfirmationAndReminder enqueues the two jobs a new booking causes.
//
// The reminder is scheduled for one hour before the window opens; a booking
// made inside that hour gets none, because a reminder that arrives after the
// thing it reminds you of is noise.
func (r *Reservations) scheduleConfirmationAndReminder(ctx context.Context, tx pgx.Tx, orgID string, row Reservation) error {
	if r.options.Jobs == nil {
		return nil
	}
	if err := r.options.Jobs.InsertTx(ctx, tx,
		ConfirmationArgs{OrgID: orgID, ReservationID: row.ID}, nil); err != nil {
		return fmt.Errorf("enqueuing the confirmation: %w", err)
	}

	// The no-show check, scheduled for the moment the window closes. Enqueued
	// in the same transaction as the booking, so it exists if and only if the
	// booking does — there is no reconciliation path to get wrong.
	if err := r.options.Jobs.InsertTx(ctx, tx,
		NoShowCheckArgs{OrgID: orgID, ReservationID: row.ID},
		&river.InsertOpts{ScheduledAt: row.EndsAt}); err != nil {
		return fmt.Errorf("enqueuing the no-show check: %w", err)
	}

	remindAt := row.StartsAt.Add(-ReminderLeadTime)
	if !remindAt.After(r.now()) {
		// A booking made inside the lead time gets no reminder: one arriving
		// after the thing it reminds you of is noise.
		return nil
	}
	if err := r.options.Jobs.InsertTx(ctx, tx,
		ReminderArgs{OrgID: orgID, ReservationID: row.ID},
		&river.InsertOpts{ScheduledAt: remindAt}); err != nil {
		return fmt.Errorf("enqueuing the reminder: %w", err)
	}
	return nil
}

// notifyNoShow writes the in-app record that a window closed uncollected.
//
// The text is rendered here and stored rendered, which is deliberate: the
// record has to say what somebody was actually told, and a stored template
// name would re-render every past notification the day the template changed.
func (r *Reservations) notifyNoShow(ctx context.Context, tx pgx.Tx, row Reservation) error {
	if r.options.Notify == nil {
		return nil
	}
	_, err := r.options.Notify.Notify(ctx, tx, notifications.New{
		RecipientID: row.HolderID,
		Kind:        NotifyNoShow,
		Title:       "Your reservation was not collected",
		Body: fmt.Sprintf("Your booking from %s to %s ended without the equipment being checked out.",
			row.StartsAt.Format(time.RFC1123), row.EndsAt.Format(time.RFC1123)),
	})
	if err != nil {
		return fmt.Errorf("notifying the holder of a no-show: %w", err)
	}
	return nil
}

// errMissingCollaborator reports a worker built without what it needs. It is a
// construction error rather than a runtime one, so a misconfigured process
// fails at startup instead of when the first job runs.
var errMissingCollaborator = errors.New("reservation: a required collaborator is missing")
