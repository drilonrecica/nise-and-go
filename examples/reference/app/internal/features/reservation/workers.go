// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"workbench/internal/platform/mail"
)

// Directory resolves a holder's email address.
//
// It is an interface rather than a dependency on the auth feature, because a
// worker that needed the whole account use case to send one message would tie
// the job queue to the identity system's shape. What the worker actually needs
// is one string.
type Directory interface {
	// EmailOf returns the address for an account, or an error when there is
	// none to send to.
	EmailOf(ctx context.Context, userID string) (string, error)
}

// Workers send the messages a reservation change causes.
//
// They are constructed with everything they need, and refuse to be built
// without it. A worker missing its mailer fails at startup rather than when
// the first job runs, which is the difference between a deploy that does not
// come up and a queue that quietly retries forever.
type Workers struct {
	reservations *Reservations
	directory    Directory
	mailer       mail.Mailer
	// from is the envelope sender.
	from string
	now  func() time.Time
}

// NewWorkers builds them.
func NewWorkers(reservations *Reservations, directory Directory, mailer mail.Mailer, from string) (*Workers, error) {
	switch {
	case reservations == nil:
		return nil, fmt.Errorf("%w: the reservations use case", errMissingCollaborator)
	case directory == nil:
		return nil, fmt.Errorf("%w: a directory to resolve addresses", errMissingCollaborator)
	case mailer == nil:
		return nil, fmt.Errorf("%w: a mailer", errMissingCollaborator)
	}
	if err := mail.ValidateAddress(from); err != nil {
		return nil, fmt.Errorf("reservation: the sender address: %w", err)
	}
	return &Workers{
		reservations: reservations,
		directory:    directory,
		mailer:       mailer,
		from:         from,
		now:          time.Now,
	}, nil
}

// WithClock returns a copy reading the time from now.
func (w *Workers) WithClock(now func() time.Time) *Workers {
	copied := *w
	copied.now = now
	return &copied
}

// The three workers, returned individually rather than registered here.
//
// Registration goes through the application's own jobs.Register, which records
// each kind so that scheduling a kind with no worker is caught at startup —
// silence that otherwise looks exactly like success. A Register method on this
// type would use river.AddWorker directly and slip past that check, which is
// the kind of shortcut that is invisible until a periodic job never runs.

// Confirmation returns the worker that sends a booking confirmation.
func (w *Workers) Confirmation() river.Worker[ConfirmationArgs] {
	return &confirmationWorker{workers: w}
}

// Reminder returns the worker that tells a holder their window is about to
// open.
func (w *Workers) Reminder() river.Worker[ReminderArgs] { return &reminderWorker{workers: w} }

// NoShowCheck returns the worker that closes out an uncollected reservation.
func (w *Workers) NoShowCheck() river.Worker[NoShowCheckArgs] { return &noShowCheckWorker{workers: w} }

// send builds and sends one message about one reservation.
//
// The reservation is re-read rather than carried in the job payload. Two
// reasons, and the second is the one that matters: a payload is a row anybody
// who can read the database can read, and a message sent after a retry should
// describe the reservation as it is rather than as it was when the job was
// enqueued.
func (w *Workers) send(ctx context.Context, orgID, reservationID, subject string, body func(Reservation) string) error {
	booking, err := w.reservations.Get(ctx, orgID, reservationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The reservation is gone. There is nothing to send and nothing
			// to retry, and River treats a returned error as retryable — so
			// this returns nil deliberately rather than failing forever.
			return nil
		}
		return fmt.Errorf("reading the reservation: %w", err)
	}

	address, err := w.directory.EmailOf(ctx, booking.HolderID)
	if err != nil {
		return fmt.Errorf("resolving the holder's address: %w", err)
	}

	return w.mailer.Send(ctx, mail.Message{
		From:    w.from,
		To:      []string{address},
		Subject: subject,
		Text:    body(booking),
	})
}

// confirmationWorker sends the booking confirmation.
type confirmationWorker struct {
	river.WorkerDefaults[ConfirmationArgs]
	workers *Workers
}

func (c *confirmationWorker) Work(ctx context.Context, job *river.Job[ConfirmationArgs]) error {
	return c.workers.send(ctx, job.Args.OrgID, job.Args.ReservationID,
		"Your reservation is confirmed",
		func(booking Reservation) string {
			return fmt.Sprintf(
				"Your booking is confirmed.\n\nFrom: %s\nUntil: %s\n\nCheck the equipment out when your window opens.",
				booking.StartsAt.Format(time.RFC1123), booking.EndsAt.Format(time.RFC1123))
		})
}

// reminderWorker tells a holder their window is about to open.
type reminderWorker struct {
	river.WorkerDefaults[ReminderArgs]
	workers *Workers
}

func (r *reminderWorker) Work(ctx context.Context, job *river.Job[ReminderArgs]) error {
	return r.workers.send(ctx, job.Args.OrgID, job.Args.ReservationID,
		"Your reservation starts soon",
		func(booking Reservation) string {
			// A reminder for a booking that has since been cancelled or
			// collected is noise, and the job was scheduled an hour before
			// anybody knew that. Checking here rather than cancelling the job
			// keeps the decision in one place.
			return fmt.Sprintf(
				"Your booking opens at %s and runs until %s.",
				booking.StartsAt.Format(time.RFC1123), booking.EndsAt.Format(time.RFC1123))
		})
}

// noShowCheckWorker closes out one reservation whose window has ended.
//
// It carries its own tenant, which is what makes it possible at all: see
// NoShowCheckArgs for why a periodic sweep cannot enumerate tenants under this
// application's row-level security, and why that is the design rather than a
// gap in it.
type noShowCheckWorker struct {
	river.WorkerDefaults[NoShowCheckArgs]
	workers *Workers
}

// Work marks the reservation a no-show if it is still merely booked.
//
// The sweep is reused rather than a single-row update written beside it,
// because the two would then be two definitions of "uncollected" that could
// drift. The cutoff is the reservation's own window end, so the sweep touches
// exactly this one — and touches nothing if it was cancelled, collected, or
// swept already by the manual path.
func (n *noShowCheckWorker) Work(ctx context.Context, job *river.Job[NoShowCheckArgs]) error {
	booking, err := n.workers.reservations.Get(ctx, job.Args.OrgID, job.Args.ReservationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Nothing to close out and nothing to retry. River treats a
			// returned error as retryable, so this returns nil deliberately.
			return nil
		}
		return fmt.Errorf("reading the reservation: %w", err)
	}
	if booking.State != StateBooked {
		return nil
	}
	if _, err := n.workers.reservations.SweepNoShows(ctx, job.Args.OrgID, booking.EndsAt); err != nil {
		return fmt.Errorf("closing out %s: %w", job.Args.ReservationID, err)
	}
	return nil
}
