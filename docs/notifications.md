# In-app notifications

The `notifications` compile-time module. A project generated without it has no
`internal/features/notifications/`, no notification tables, and no code that
mentions either.

## Two tables, and why

`notifications` is **what happened**: one row per thing a person should know
about. `notification_deliveries` is **what was attempted about it**: one row
per channel per notification, each with its own outcome.

That split is the whole design. It is what makes

> delivery failure does not erase the underlying notification

a property of the schema rather than a rule somebody has to remember. An SMTP
server that is down produces a failed delivery row; the notification is still
there, still unread, and still shown the next time its recipient looks.

In-app delivery has no delivery row. The notification *is* the in-app
delivery, and a row that would be `sent` the instant it was written is
bookkeeping about nothing.

## Notify takes a transaction

```go
err := transactor.Within(ctx, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
    if err := reports.MarkReady(ctx, tx, id); err != nil {
        return err
    }
    notification, err := notifications.Notify(ctx, tx, notifications.New{
        RecipientID: report.OwnerID,
        Kind:        "report.ready",
        Title:       "Your report is ready",
        Body:        "The report you asked for has finished generating.",
        Data:        json.RawMessage(`{"report_id":"…"}`),
        Channels:    []string{"email"},
    })
    if err != nil {
        return err
    }
    return notifications.Enqueue(ctx, tx, jobClient, notification, []string{"email"})
})
```

`Notify` takes a `pgx.Tx`, not a context it could start its own transaction
from. A notification that exists for a change that rolled back is a lie told
to a person; one missing after a change committed is a person not told at all.
Taking the transaction as a parameter makes writing it alongside the change
the only way to call it — there is no overload that could commit
independently.

The delivery rows go in the same transaction, so a job cannot find a delivery
for a notification that does not exist yet. So do the delivery *jobs*, through
`Enqueue`, for the same reason.

## Rendered text, not a template reference

`Title` and `Body` are already rendered and already localized.

Storing a template name and its arguments instead would mean every past
notification re-renders when a template changes — so the record of what
somebody was told would stop matching what they were actually told. That is a
bad property for an audit trail and a worse one for a person asking what a
message said.

`Kind` is the machine-readable half: a stable name like `report.ready` that a
frontend switches on to pick an icon or a link. It is a name the application
owns, never a Go type path, so renaming a type cannot orphan what is stored.

## Channels

```go
type Channel interface {
    Name() string
    Deliver(ctx context.Context, notification Notification) error
}
```

Channels are registered once, at startup, in `internal/app/notifications.go`.
There is no registry and no discovery. A channel not in that call cannot be
named: `Notify` refuses an unknown channel before writing anything, so a typo
is a refusal rather than a notification nothing will ever deliver.

A generated application ships the `email` channel as a **stub that returns an
error**, because how a notification becomes an email — which address, what
subject, what layout — is an application decision. It is a refusal rather than
a silent success on purpose: a channel that reports delivery without
delivering is worse than one that does not exist, because the delivery row
would say `sent`.

## Delivery is one job per channel

`notification_deliver` carries a notification id and a channel name.

One job per channel rather than one job that walks every channel, because two
channels have unrelated failure modes and unrelated retry schedules — a single
job would either retry a channel that had succeeded or give up on one that
would have worked. The unique constraint on `(notification_id, channel)` is
what makes the split safe: a duplicate job finds the row already `sent` and
does nothing.

The worker never modifies the notification, in either direction. A successful
delivery does not mark it read; a failed one does not remove it.

A delivery stays `pending` while it is being retried and becomes `failed` only
on the last attempt, because `failed` means the attempt is over and only the
last attempt knows that. A channel that is no longer configured in this
deployment is a terminal failure — retrying cannot fix a deployment that does
not have it.

## Ownership

`List`, `Unread`, `MarkRead`, and `MarkAllRead` are all scoped to a recipient.
A refusal for "not yours" is the same error value as "does not exist", because
whether an identifier exists is itself information.

Marking read twice keeps the first timestamp: when somebody first saw it is
the more useful fact.

## Paging

`List` pages by the `CreatedAt` of the last row on the previous page, not by
an offset. An offset skips or repeats rows when a new notification arrives
between two pages — which, for a notification list, is the single most likely
thing to happen.
