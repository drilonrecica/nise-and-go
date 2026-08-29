# Database transactions

Business use cases own transaction boundaries. HTTP handlers decode and
translate; feature-local sqlc query sets execute against the `DBTX` they are
given; neither layer begins or commits a transaction.

The shared `runtime/transaction` package implements driver-independent
lifecycle. Generated `internal/platform/database.Transactor` maps its closed
options to pgx and passes the real `pgx.Tx` into the callback:

```go
type CreateInvoice struct {
	transactions *database.Transactor
}

func (uc *CreateInvoice) Execute(ctx context.Context, input Input) error {
	return uc.transactions.Within(ctx, transaction.Options{
		Isolation: transaction.Serializable,
		Access:    transaction.ReadWrite,
	}, func(ctx context.Context, tx pgx.Tx) error {
		queries := store.New(tx)
		return queries.CreateInvoice(ctx, store.CreateInvoiceParams{/* ... */})
	})
}
```

The composition root constructs the transactor beside the primary pool and
passes it to feature constructors explicitly. There is no global runner and no
transaction value hidden in context. The callback context carries only a
private nested-use marker and must be propagated to called use cases.

## Lifecycle contract

- Successful work commits once.
- A work error rolls back. If rollback also fails, the returned error preserves
  both causes for `errors.Is`/`errors.As`.
- A canceled context rolls back rather than committing work that returned nil.
- A commit failure is returned and followed by best-effort rollback.
- A panic receives bounded best-effort rollback and is re-panicked unchanged.
- Rollback cleanup is detached from an already-canceled business context but
  has a five-second default timeout, so cleanup is possible without hanging
  shutdown indefinitely.

`Within` rejects an unknown isolation/access value before opening a
transaction. Default options defer to PostgreSQL configuration; explicit
choices are read committed, repeatable read, serializable, read-write, and
read-only.

Nested `Within` calls made with the callback context fail. This is deliberate:
the helper does not invent savepoints or let an inner use case commit
independently. Compose the inner operation against the existing `pgx.Tx`,
usually through a narrow interface declared by the calling feature. Replacing
the callback context with an ancestor defeats this detection and violates the
contract.

The exact rationale and rejected alternatives are in
[ADR 0015](adr/0015-use-case-owned-transactions.md).
