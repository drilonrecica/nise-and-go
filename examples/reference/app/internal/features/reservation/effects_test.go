// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	"github.com/drilonrecica/nise-and-go/runtime/authz"

	"workbench/internal/features/audit"
	"workbench/internal/features/notifications"
	"workbench/internal/platform/authorization"
	"workbench/internal/platform/mail"
)

// The property every test in this file is about: an audit record, a
// notification, and an enqueued job become visible if and only if the business
// change committed. That is not a convention a reviewer has to enforce — every
// collaborator is called inside the transaction that made the change, so a
// rollback takes all three with it and there is no compensating path.

// recordingEnqueuer captures what was enqueued, and can be made to fail.
type recordingEnqueuer struct {
	enqueued  []river.JobArgs
	scheduled []time.Time
	fail      error
}

func (e *recordingEnqueuer) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error {
	if e.fail != nil {
		return e.fail
	}
	e.enqueued = append(e.enqueued, args)
	if opts != nil {
		e.scheduled = append(e.scheduled, opts.ScheduledAt)
	} else {
		e.scheduled = append(e.scheduled, time.Time{})
	}
	return nil
}

func (e *recordingEnqueuer) kinds() []string {
	out := make([]string, 0, len(e.enqueued))
	for _, args := range e.enqueued {
		out = append(out, args.Kind())
	}
	return out
}

// capturingMailer records the messages sent through it.
type capturingMailer struct{ sent []mail.Message }

func (m *capturingMailer) Send(_ context.Context, message mail.Message) error {
	m.sent = append(m.sent, message)
	return nil
}

// staticDirectory resolves every account to one address.
type staticDirectory struct {
	address string
	fail    error
}

func (d staticDirectory) EmailOf(context.Context, string) (string, error) {
	if d.fail != nil {
		return "", d.fail
	}
	return d.address, nil
}

// withEffects rebuilds the harness's use case with the given collaborators.
func (h *harness) withEffects(t *testing.T, options Options) *Reservations {
	t.Helper()

	built, err := New(h.transactor, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.WithClock(func() time.Time { return testClock })
}

func (h *harness) recorder(t *testing.T) *audit.Recorder {
	t.Helper()

	recorder, err := audit.NewRecorder(h.transactor)
	if err != nil {
		t.Fatalf("audit.NewRecorder: %v", err)
	}
	return recorder
}

func (h *harness) notifier(t *testing.T) *notifications.Notifications {
	t.Helper()

	notifier, err := notifications.NewNotifications(h.transactor)
	if err != nil {
		t.Fatalf("notifications.NewNotifications: %v", err)
	}
	return notifier
}

// reader returns a context holding audit.read, which the query API requires.
//
// Building one explicitly for every read is the point: the context a caller
// gets by not thinking about it is the one that is denied, and these tests
// should go through the same door a person reading the log does.
func (h *harness) reader(t *testing.T) context.Context {
	t.Helper()

	granted, err := authz.NewSet(authorization.AuditRead)
	if err != nil {
		t.Fatalf("authz.NewSet: %v", err)
	}
	return authorization.WithGrants(h.ctx, authorization.Grants{
		UserID:      h.holder,
		Permissions: granted,
	})
}

// auditRecords reads the log for one subject, through the recorder's own
// filter rather than by querying the table, so the test exercises the path a
// person reading the audit log would take.
func (h *harness) auditRecords(t *testing.T, recorder *audit.Recorder, subjectID string) []audit.Record {
	t.Helper()

	records, err := recorder.List(h.reader(t), audit.Filter{SubjectKind: subjectKind, SubjectID: subjectID}, audit.Position{}, 100)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	return records
}

// --- The transactional guarantee ------------------------------------------

// The one that matters. A business change that rolls back must take its audit
// record and its enqueued jobs with it — otherwise the log records something
// that did not happen and a worker sends a confirmation for a booking nobody
// has.
func TestARolledBackBookingLeavesNoRecordAndNoJob(t *testing.T) {
	h := newHarness(t)
	recorder := h.recorder(t)
	enqueuer := &recordingEnqueuer{}
	reservations := h.withEffects(t, Options{Audit: recorder, Jobs: enqueuer})

	// A booking that will be refused by the exclusion constraint, so the
	// transaction rolls back after the effects have already run inside it.
	first, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(time.Hour), EndsAt: testClock.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := len(h.auditRecords(t, recorder, first.ID))

	_, err = reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(2 * time.Hour), EndsAt: testClock.Add(4 * time.Hour),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}

	// The refused booking left nothing behind. Its audit record would have a
	// subject identifier nobody can resolve, and its confirmation job would
	// send mail about a reservation that does not exist.
	records, err := recorder.List(h.reader(t), audit.Filter{SubjectKind: subjectKind}, audit.Position{}, 100)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	if len(records) != before {
		t.Errorf("the log holds %d record(s) for reservations, want the %d from the booking that committed", len(records), before)
	}
}

// The job's side of the same guarantee, proved through the real job table
// rather than a fake: a rolled-back transaction leaves no row in river_job.
func TestARolledBackBookingEnqueuesNothingInTheRealQueue(t *testing.T) {
	h := newHarness(t)
	client := h.jobClient(t)
	reservations := h.withEffects(t, Options{Jobs: client})

	if _, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(time.Hour), EndsAt: testClock.Add(3 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	committed := h.queuedKinds(t)

	_, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(2 * time.Hour), EndsAt: testClock.Add(4 * time.Hour),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}

	after := h.queuedKinds(t)
	if len(after) != len(committed) {
		t.Errorf("the queue holds %d job(s) after a rolled-back booking and %d after the one that committed",
			len(after), len(committed))
	}
}

// And the other direction: a collaborator that fails takes the business change
// with it. An audit log that can be skipped by making the recorder fail is not
// an audit log.
func TestAFailedEffectRollsBackTheBooking(t *testing.T) {
	h := newHarness(t)
	enqueuer := &recordingEnqueuer{fail: errors.New("the queue is unavailable")}
	reservations := h.withEffects(t, Options{Jobs: enqueuer})

	_, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(time.Hour), EndsAt: testClock.Add(3 * time.Hour),
	})
	if err == nil {
		t.Fatal("a booking committed even though its job could not be enqueued")
	}

	page, listErr := reservations.List(h.ctx, h.orgID, ListOptions{})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if page.Total != 0 {
		t.Errorf("the database holds %d reservation(s) after a failed enqueue, want 0", page.Total)
	}
}

// --- Audit ---------------------------------------------------------------

func TestEveryTransitionIsAudited(t *testing.T) {
	h := newHarness(t)
	recorder := h.recorder(t)
	reservations := h.withEffects(t, Options{Audit: recorder})

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(-time.Hour), EndsAt: testClock.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
		t.Fatalf("CheckOut: %v", err)
	}
	if _, err := reservations.Return(h.ctx, h.orgID, booked.ID, h.holderActor(), "", ""); err != nil {
		t.Fatalf("Return: %v", err)
	}

	records := h.auditRecords(t, recorder, booked.ID)
	got := make([]string, 0, len(records))
	for _, record := range records {
		got = append(got, record.Action)
	}
	// Newest first, which is the order the log lists in.
	want := []string{ActionReturned, ActionCheckedOut, ActionCreated}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the log holds %v, want %v", got, want)
	}
	for _, record := range records {
		if record.ActorID != h.holder {
			t.Errorf("%s was recorded with actor %q, want %q", record.Action, record.ActorID, h.holder)
		}
		if record.Detail["resource_id"] != h.resource.ID {
			t.Errorf("%s does not name the resource", record.Action)
		}
	}
}

// A refused transition is not recorded as having happened. The log says what
// occurred, and a record for a check-out that was refused would be a record of
// something that did not.
func TestARefusedTransitionIsNotAudited(t *testing.T) {
	h := newHarness(t)
	recorder := h.recorder(t)
	reservations := h.withEffects(t, Options{Audit: recorder})

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(2 * time.Hour), EndsAt: testClock.Add(4 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("err = %v, want ErrNotStarted", err)
	}

	for _, record := range h.auditRecords(t, recorder, booked.ID) {
		if record.Action == ActionCheckedOut {
			t.Error("a refused check-out was recorded as having happened")
		}
	}
}

// The sweep has no person behind it, and recording the system as a user would
// put an empty identifier in a column somebody later joins on.
func TestTheSweepIsAuditedAsTheSystem(t *testing.T) {
	h := newHarness(t)
	recorder := h.recorder(t)
	reservations := h.withEffects(t, Options{Audit: recorder})

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(-5 * time.Hour), EndsAt: testClock.Add(-3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reservations.SweepNoShows(h.ctx, h.orgID, testClock); err != nil {
		t.Fatalf("SweepNoShows: %v", err)
	}

	var found bool
	for _, record := range h.auditRecords(t, recorder, booked.ID) {
		if record.Action != ActionNoShow {
			continue
		}
		found = true
		if record.ActorKind != audit.ActorSystem {
			t.Errorf("the sweep was recorded with actor kind %q, want %q", record.ActorKind, audit.ActorSystem)
		}
		if record.ActorID != "" {
			t.Errorf("the sweep was recorded with actor %q, want none", record.ActorID)
		}
	}
	if !found {
		t.Error("the sweep recorded no audit event")
	}
}

// --- Jobs -----------------------------------------------------------------

func TestABookingEnqueuesItsThreeJobs(t *testing.T) {
	h := newHarness(t)
	enqueuer := &recordingEnqueuer{}
	reservations := h.withEffects(t, Options{Jobs: enqueuer})

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(5 * time.Hour), EndsAt: testClock.Add(7 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{ConfirmationArgs{}.Kind(), NoShowCheckArgs{}.Kind(), ReminderArgs{}.Kind()}
	if strings.Join(enqueuer.kinds(), ",") != strings.Join(want, ",") {
		t.Fatalf("enqueued %v, want %v", enqueuer.kinds(), want)
	}
	// The no-show check runs when the window closes; the reminder an hour
	// before it opens. A schedule that was wrong by an hour would still look
	// like a working system until somebody missed a booking.
	if !enqueuer.scheduled[1].Equal(booked.EndsAt) {
		t.Errorf("the no-show check is scheduled for %s, want the window's end %s", enqueuer.scheduled[1], booked.EndsAt)
	}
	if !enqueuer.scheduled[2].Equal(booked.StartsAt.Add(-ReminderLeadTime)) {
		t.Errorf("the reminder is scheduled for %s, want %s", enqueuer.scheduled[2], booked.StartsAt.Add(-ReminderLeadTime))
	}
}

// A booking made inside the lead time gets no reminder. One arriving after the
// thing it reminds you of is noise, and noise is how people learn to ignore
// notifications.
func TestABookingInsideTheLeadTimeGetsNoReminder(t *testing.T) {
	h := newHarness(t)
	enqueuer := &recordingEnqueuer{}
	reservations := h.withEffects(t, Options{Jobs: enqueuer})

	if _, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(15 * time.Minute), EndsAt: testClock.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, kind := range enqueuer.kinds() {
		if kind == (ReminderArgs{}).Kind() {
			t.Error("a booking starting inside the reminder lead time still enqueued a reminder")
		}
	}
}

// --- Notifications --------------------------------------------------------

func TestANoShowNotifiesItsHolder(t *testing.T) {
	h := newHarness(t)
	notifier := h.notifier(t)
	reservations := h.withEffects(t, Options{Notify: notifier})

	if _, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(-5 * time.Hour), EndsAt: testClock.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reservations.SweepNoShows(h.ctx, h.orgID, testClock); err != nil {
		t.Fatalf("SweepNoShows: %v", err)
	}

	listed, err := notifier.List(h.ctx, h.holder, 10, time.Time{})
	if err != nil {
		t.Fatalf("notifications.List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("the holder has %d notification(s), want 1", len(listed))
	}
	if listed[0].Kind != NotifyNoShow {
		t.Errorf("kind = %q, want %q", listed[0].Kind, NotifyNoShow)
	}
	// Stored rendered, so the record says what somebody was actually told
	// rather than re-rendering the day the wording changes.
	if listed[0].Title == "" || listed[0].Body == "" {
		t.Error("the notification was stored without its text")
	}
}

// --- Mail -----------------------------------------------------------------

func TestTheConfirmationWorkerSendsOneMessageToTheHolder(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})
	mailer := &capturingMailer{}

	workers, err := NewWorkers(reservations, staticDirectory{address: "holder@example.com"}, mailer, "workbench@example.com")
	if err != nil {
		t.Fatalf("NewWorkers: %v", err)
	}

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(5 * time.Hour), EndsAt: testClock.Add(7 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	worker := &confirmationWorker{workers: workers}
	if err := worker.Work(h.ctx, &river.Job[ConfirmationArgs]{
		Args: ConfirmationArgs{OrgID: h.orgID, ReservationID: booked.ID},
	}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d message(s), want 1", len(mailer.sent))
	}
	message := mailer.sent[0]
	if len(message.To) != 1 || message.To[0] != "holder@example.com" {
		t.Errorf("sent to %v, want the holder", message.To)
	}
	if message.Text == "" {
		t.Error("the message has no plain-text body; a message with only HTML is unreadable to a screen reader that gave up on the HTML")
	}
	if err := message.Validate(); err != nil {
		t.Errorf("the message the worker built is not valid: %v", err)
	}
}

// A job for a reservation that no longer exists has nothing to send and
// nothing to retry. River treats a returned error as retryable, so returning
// one here would retry forever against a row that will never come back.
func TestAJobForAVanishedReservationSucceedsWithoutSending(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})
	mailer := &capturingMailer{}

	workers, err := NewWorkers(reservations, staticDirectory{address: "holder@example.com"}, mailer, "workbench@example.com")
	if err != nil {
		t.Fatalf("NewWorkers: %v", err)
	}
	worker := &confirmationWorker{workers: workers}
	err = worker.Work(h.ctx, &river.Job[ConfirmationArgs]{
		Args: ConfirmationArgs{OrgID: h.orgID, ReservationID: "44444444-4444-4444-4444-444444444444"},
	})
	if err != nil {
		t.Fatalf("Work: %v, want no error and no send", err)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("sent %d message(s) for a reservation that does not exist", len(mailer.sent))
	}
}

// The no-show check acts only on a reservation still merely booked, so a
// cancelled or collected one is untouched by the job scheduled at booking
// time — which is the whole reason it can be scheduled that early.
func TestTheNoShowCheckLeavesACollectedReservationAlone(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})
	mailer := &capturingMailer{}
	workers, err := NewWorkers(reservations, staticDirectory{address: "holder@example.com"}, mailer, "workbench@example.com")
	if err != nil {
		t.Fatalf("NewWorkers: %v", err)
	}
	workers = workers.WithClock(func() time.Time { return testClock })

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(-4 * time.Hour), EndsAt: testClock.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := reservations.CheckOut(h.ctx, h.orgID, booked.ID, h.holderActor()); err != nil {
		t.Fatalf("CheckOut: %v", err)
	}

	worker := &noShowCheckWorker{workers: workers}
	if err := worker.Work(h.ctx, &river.Job[NoShowCheckArgs]{
		Args: NoShowCheckArgs{OrgID: h.orgID, ReservationID: booked.ID},
	}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	got, err := reservations.Get(h.ctx, h.orgID, booked.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateCheckedOut {
		t.Errorf("state = %q; the no-show check marked a collected reservation", got.State)
	}
}

// And it does mark one that was never collected.
func TestTheNoShowCheckMarksAnUncollectedReservation(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})
	mailer := &capturingMailer{}
	workers, err := NewWorkers(reservations, staticDirectory{address: "holder@example.com"}, mailer, "workbench@example.com")
	if err != nil {
		t.Fatalf("NewWorkers: %v", err)
	}

	booked, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(-4 * time.Hour), EndsAt: testClock.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	worker := &noShowCheckWorker{workers: workers}
	if err := worker.Work(h.ctx, &river.Job[NoShowCheckArgs]{
		Args: NoShowCheckArgs{OrgID: h.orgID, ReservationID: booked.ID},
	}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	got, err := reservations.Get(h.ctx, h.orgID, booked.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateNoShow {
		t.Errorf("state = %q, want %q", got.State, StateNoShow)
	}
}

// A worker missing a collaborator fails at construction, so a misconfigured
// process does not come up rather than queueing work it can never do.
func TestWorkersRefuseToBeBuiltWithoutWhatTheyNeed(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})
	mailer := &capturingMailer{}
	directory := staticDirectory{address: "holder@example.com"}

	cases := map[string]func() (*Workers, error){
		"no use case":  func() (*Workers, error) { return NewWorkers(nil, directory, mailer, "a@example.com") },
		"no directory": func() (*Workers, error) { return NewWorkers(reservations, nil, mailer, "a@example.com") },
		"no mailer":    func() (*Workers, error) { return NewWorkers(reservations, directory, nil, "a@example.com") },
		"bad sender":   func() (*Workers, error) { return NewWorkers(reservations, directory, mailer, "not an address") },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := build(); err == nil {
				t.Error("the workers were built anyway")
			}
		})
	}
}

// A transaction is required for every effect, and the collaborators are
// optional — so a use case built with none of them still works. This is what
// lets a web process that never runs a worker create reservations.
func TestTheUseCaseWorksWithNoCollaborators(t *testing.T) {
	h := newHarness(t)
	reservations := h.withEffects(t, Options{})

	if _, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(time.Hour), EndsAt: testClock.Add(3 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// transactionOptionsUsed keeps the effects honest about running inside the
// caller's transaction rather than opening their own.
//
// A collaborator that opened its own transaction would still make every test
// above pass on the happy path, and would silently break the rollback
// guarantee — the one property this whole file exists for.
func TestEffectsRunInsideTheCallersTransaction(t *testing.T) {
	h := newHarness(t)

	var seen []pgx.Tx
	spy := transactionSpy{onCall: func(tx pgx.Tx) { seen = append(seen, tx) }}
	reservations := h.withEffects(t, Options{Audit: spy, Jobs: spy, Notify: spy})

	if _, err := reservations.Create(h.ctx, h.orgID, Request{
		ResourceID: h.resource.ID, HolderID: h.holder,
		StartsAt: testClock.Add(time.Hour), EndsAt: testClock.Add(3 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(seen) < 2 {
		t.Fatalf("the effects were called %d time(s); a booking records and enqueues", len(seen))
	}
	for i, tx := range seen[1:] {
		if tx != seen[0] {
			t.Errorf("effect %d ran in a different transaction from the first", i+1)
		}
	}
}

// transactionSpy satisfies every collaborator interface and records the
// transaction it was handed.
type transactionSpy struct{ onCall func(pgx.Tx) }

func (s transactionSpy) RecordWithin(_ context.Context, tx pgx.Tx, _ audit.Event) (audit.Record, error) {
	s.onCall(tx)
	return audit.Record{}, nil
}

func (s transactionSpy) InsertTx(_ context.Context, tx pgx.Tx, _ river.JobArgs, _ *river.InsertOpts) error {
	s.onCall(tx)
	return nil
}

func (s transactionSpy) Notify(_ context.Context, tx pgx.Tx, _ notifications.New) (notifications.Notification, error) {
	s.onCall(tx)
	return notifications.Notification{}, nil
}

var _ = transaction.Options{}
