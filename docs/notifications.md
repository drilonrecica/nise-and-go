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

## Live updates, and the polling fallback

`GET /api/v1/notifications/stream` is a server-sent-event stream carrying one
event kind:

```
event: unread
data: {"count":3}
```

**It carries no content.** A live event says "something changed for you"; the
client then performs the ordinary authorized read. That is not a
simplification — it is what keeps the authorization check in one place. A
stream that pushed the notification itself would be a second read path with
its own permission logic, and the two would eventually disagree.

### How it works

The notifications migration installs an `AFTER INSERT` trigger that calls
`pg_notify('notification', …)` with the recipient's id and nothing else.

PostgreSQL's `NOTIFY` is **transactional**: a notification queued inside a
transaction is delivered when that transaction commits and discarded when it
rolls back. That is exactly the semantics needed here, and it is why live
delivery needs no outbox table, no polling loop, and no reconciliation — the
database already guarantees a listener hears about a row if and only if that
row exists.

The payload is only the recipient because a `NOTIFY` payload is capped at 8000
bytes, is visible to anything permitted to `LISTEN` on the channel, and would
skip the authorization the read performs.

One process holds **one** `LISTEN` connection and fans out to its own
subscribers. A connection per client would cap concurrent viewers at the
database's connection limit — a much smaller number than a deployment expects
to serve — and would spend a pooled connection on waiting.

**The listener runs in the process that serves the endpoint**, which means the
`web` component, not the worker. The fan-out is in-process: a subscriber
registered by an HTTP handler is only ever woken by the listener in that same
process. Started in the worker instead, a deployment splitting `web` and
`worker` would mount the endpoint on processes whose fan-out nothing feeds —
clients would connect, subscribe, and silently receive nothing forever, while
the worker held a `LISTEN` connection with no subscribers to wake. It was
written that way first; the security review asked which goroutine owned it.

It holds one connection from the application pool for the process lifetime.
With the default `DB_MAX_CONNS=10` that is a tenth of the pool. Account for it
when sizing, or set `NOTIFICATIONS_SSE=false` on a deployment that does not
want to.

### What it deliberately does

- **Ends every stream after thirty minutes.** A stream that never ends is a
  stream that never re-authenticates: a session revoked while a tab is open
  would keep receiving updates for as long as that tab stayed open. The client
  reconnects, and the reconnection goes through the ordinary session check.
- **Sends the current count immediately on connect**, so a client connecting
  after a notification arrived is not left waiting for the next one.
- **Drops a signal rather than blocking** when a subscriber's buffer is full.
  The signal carries no content, so a client that missed three and receives
  the fourth performs exactly the same read — and a listener that could block
  would let one slow client stop delivery for everybody.
- **Reconnects rather than failing** when the database connection drops. A
  live update is the least important thing in the process to lose: every
  client falls back to polling on its own, having noticed nothing.
- **Refuses a response writer that cannot flush**, with a 501. Without
  flushing the response arrives all at once when the handler returns — which
  looks like a working endpoint in a test and never delivers a live event.

### Turning it off

`NOTIFICATIONS_SSE=false` is a **supported configuration, not a degraded
one**. The stream is then not mounted at all, so a client gets a 404 and falls
back immediately rather than holding open a connection that delivers nothing.

> **The polling endpoints do not exist yet.** This module ships a complete
> use case, a table, a delivery worker, and the stream — and no HTTP surface
> for reading notifications. A generated project therefore has no way for a
> browser to list them, count the unread ones, or mark one read, whether the
> stream is on or off. Building that surface is the application's job today;
> `internal/features/notifications` has `List`, `Unread`, `MarkRead`, and
> `MarkAllRead` waiting for it.
>
> This is a V0.1 gap rather than a design decision. See
> [project status](project-status.md#what-is-not-finished).

### Why it is not in the OpenAPI document

An OpenAPI document describes request and response *bodies*. A server-sent
event stream has no body in that sense: it has an unbounded sequence of framed
events over one response. Every attempt to express that in OpenAPI produces a
description generated code cannot use and a reader cannot trust.

So the stream is mounted beside the generated operations, in
`internal/app/notifications.go`, and documented here.

The intended shape is that the polling endpoints serving the same information
are in the document as usual — so the contract a client generates against
covers everything and the stream is an optimisation on top of it. Those
endpoints are the part not yet written; see the note above.
