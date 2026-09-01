// Generated once by nise; owned by this application. Nise will not overwrite it.

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"workbench/internal/features/auth"
	"workbench/internal/features/reservation"
	"workbench/internal/platform/jobs"
	"workbench/internal/platform/mail"
)

// The wiring Workbench adds, kept out of app.go so that reading app.go is
// still reading the framework's startup sequence.

// accountDirectory resolves a holder's email address for the mail workers.
//
// It is an adapter rather than the workers depending on the auth feature
// directly: what a worker needs is one string, and a job queue that imported
// the identity system's whole shape would be tied to every change in it.
type accountDirectory struct {
	accounts *auth.Accounts
}

// EmailOf implements reservation.Directory.
func (d accountDirectory) EmailOf(ctx context.Context, userID string) (string, error) {
	account, err := d.accounts.FindByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("resolving the account: %w", err)
	}
	if account.Email == "" {
		return "", fmt.Errorf("account %s has no address to send to", userID)
	}
	return account.Email, nil
}

// deferredEnqueuer breaks a genuine construction cycle.
//
// The reservation use case enqueues jobs, so it needs a job client. The job
// client is built from a registry of workers, and the reservation workers need
// the use case. One of the three has to be constructed before something it
// depends on exists, and this is the smallest place to absorb that: the use
// case is given an enqueuer whose delegate is set the moment the client
// exists.
//
// It fails loudly rather than silently. An enqueue before the delegate is set
// would be a job that was never queued for a change that did commit — the
// exact failure the transactional-enqueue design exists to prevent — so it
// returns an error, which rolls the transaction back.
type deferredEnqueuer struct {
	delegate reservation.Enqueuer
}

// InsertTx implements reservation.Enqueuer.
func (d *deferredEnqueuer) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error {
	if d.delegate == nil {
		return errors.New("app: the job client is not wired yet; this enqueue would have been lost")
	}
	return d.delegate.InsertTx(ctx, tx, args, opts)
}

// registerReservationJobs adds Workbench's own workers to the registry.
//
// Registration is explicit and in one place rather than each worker
// registering itself from an init(): a job kind that exists only because a
// package happened to be imported is one nobody can find from the registry.
func registerReservationJobs(
	registry *jobs.Registry,
	reservations *reservation.Reservations,
	accounts *auth.Accounts,
	mailer mail.Mailer,
	from string,
	logger *slog.Logger,
) error {
	workers, err := reservation.NewWorkers(reservations, accountDirectory{accounts: accounts}, mailer, from)
	if err != nil {
		return fmt.Errorf("building the reservation workers: %w", err)
	}
	jobs.Register(registry, workers.Confirmation())
	jobs.Register(registry, workers.Reminder())
	jobs.Register(registry, workers.NoShowCheck())
	logger.Debug("reservation jobs registered",
		slog.Any("kinds", []string{
			reservation.ConfirmationArgs{}.Kind(),
			reservation.ReminderArgs{}.Kind(),
			reservation.NoShowCheckArgs{}.Kind(),
		}))
	return nil
}
