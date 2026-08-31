# Idempotency for Sensitive Commands

A client that never learns whether its request arrived — a timeout, a dropped
connection, a proxy that retried on its own — has to choose between losing the
command and running it twice. An idempotency key removes that choice: the
second attempt returns the first attempt's recorded response instead of
charging the card again.

This applies to commands with an effect worth not repeating. An ordinary `GET`
is already idempotent and needs none of this.

## The client contract

Send `Idempotency-Key` on the request. Reuse the same key for every retry of
the same request, and generate a new one for a new request. A UUID or a ULID is
the usual choice.

| Refusal | Status | Code | What to do |
|---|---:|---|---|
| Missing or malformed key | `400` | `invalid_idempotency_key` | Send a key of 8–255 printable ASCII characters. |
| The same key is still in flight | `409` | `idempotency_conflict` | Retry shortly; the first attempt has not finished. |
| The key was used for a different request | `422` | `idempotency_key_reuse` | Use a new key. Retrying this one will never work. |

A replayed response is byte-identical to the original, including its status.
The window is bounded: after the retention period the key is free again and the
command would run a second time, so retries belong within it, not a week later.

**A failed command is not a permanent refusal.** The key promises at-most-once
for a command that *succeeded*. If the command returned an error, nothing was
recorded and the same key runs it again — which is what a client retrying a
`500` actually wants.

## How it works

The claim and the command share one transaction. That is the whole design:

- A row exists only if the command committed, so a rolled-back command leaves
  nothing to reconcile.
- There is no in-progress state, no reaper for abandoned claims, and no way for
  a crash to poison a key permanently.
- A visible row is always a completed one, so a replay never has to guess.

Concurrency is resolved by a transaction-scoped advisory lock rather than by
waiting on the unique index. Two simultaneous attempts with the same key
produce exactly one execution and one immediate `409`, instead of one execution
and one request held open for as long as the first takes. PostgreSQL releases
the lock at commit or rollback, so there is nothing to unwind.

The recorded fingerprint is compared *before* the response is returned:
replaying one request's response to a different request would be worse than
refusing either.

## Implementing a command

```go
key, err := idempotencyKey(r)                       // validates the header
fingerprint, err := idempotencyFingerprint(r, body) // method + path + decoded body

result, err := executor.Do(ctx, "payments.create", key, fingerprint,
        func(ctx context.Context, tx pgx.Tx) (idempotency.Response, error) {
                // every write uses tx, so effects and record commit together
        })
if err != nil {
        problem.Handler(idempotencyProblem(err))(w, r, err)
        return
}
```

Rules the helpers exist to keep:

- **Every write goes through the callback's `pgx.Tx`.** A write on the pool
  instead survives a rollback, which defeats the entire mechanism.
- **The scope names the command, not the resource.** Two different commands may
  share a client's key without colliding. Anything outside the body that must
  not change between attempts — the acting user, the tenant — belongs in the
  scope, not in the fingerprint.
- **`Do` owns the transaction.** It cannot be called from inside another one;
  the runner rejects nesting.
- **The command must be the whole unit.** Side effects outside PostgreSQL
  (sending mail, calling a payment provider) do not roll back. Enqueue them as
  transactional jobs so they commit with the record.
- **Sweep expired rows.** `DeleteExpired` removes a bounded batch and reports
  how many; call it in a loop until it returns zero.

## Storage

`db/migrations/00002_idempotency_keys.sql` creates one table keyed by
`(scope, idempotency_key)`. It stores the request fingerprint, the response
status and body, and the creation and expiry instants, with database-level
checks on every length and range so a bug in application code cannot record a
row the replay path could not serve. Retention defaults to 24 hours and is
capped at 7 days; a recorded body is capped at 1 MiB, matching the transport
response limit, because a body too large to send is also too large to replay.

## Related

- [OpenAPI server bindings and frontend client](openapi.md)
- [Database transactions](database-transactions.md)
- [Database migrations](database-migrations.md)
