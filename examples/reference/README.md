# Workbench — the Nise & Go reference application

A shared-equipment reservation and checkout system: organizations own bookable
resources, members reserve them for a time window, and a reservation is checked
out and returned.

It exists to prove the whole V0.1 surface works together, in one application,
with nothing mocked. Every capability the framework offers appears here because
the domain genuinely needs it — not because a checklist said so.

**Why this domain, and why it lives here as an overlay rather than a committed
tree:** [ADR 0028](../../docs/adr/0028-reference-application.md).

---

## What it does

An organization — a lab, a workshop, a production team — owns **resources**:
instruments, machines, cameras, rooms. Its members make **reservations** for a
time window. When the window arrives they **check the resource out**, and when
they are finished they **return** it.

That is the whole product. It is deliberately small, and deliberately not a
to-do list: every one of the following is a real requirement of this domain
rather than a demonstration bolted onto it.

### The state machine

```
                  cancel
      ┌──────────────────────────────┐
      │                              ▼
   booked ──check-out──▶ checked_out ──return──▶ returned
      │
      └──(window ended, never collected)──▶ no_show
```

`check-out` and `return` are the **non-CRUD domain commands**. They are not
field updates: each has preconditions the API enforces and the database
backs up, each writes an audit record, and each may enqueue work.

| Command | Refuses when |
|---|---|
| `check-out` | The window has not started; the reservation is not `booked`; the caller is not the holder and lacks `reservations.manage` |
| `return` | The reservation is not `checked_out` |
| `cancel` | The reservation is already `checked_out`, `returned`, or `no_show` |

Both are **idempotent** through the framework's idempotency keys: a retried
`check-out` returns the first result rather than failing or double-recording.

### Double-booking is prevented by the database

Two people reserving the same instrument for overlapping windows is the
interesting failure in this domain, and it is *not* prevented by reading before
writing — that check passes for both under concurrency.

```sql
ALTER TABLE reservations
  ADD CONSTRAINT reservations_no_overlap
  EXCLUDE USING gist (
    resource_id WITH =,
    during      WITH &&
  ) WHERE (state IN ('booked', 'checked_out'));
```

The application translates the constraint violation into a `409` Problem
Details response naming the conflicting window. The test that matters is two
concurrent transactions against a real PostgreSQL: exactly one commits.

This is the reference application's central argument about the framework:
**correctness that the database can hold, the database holds.**

## Which capabilities it exercises, and where

| Capability | Where it appears |
|---|---|
| Authentication | Invitation enrollment, sign-in, session rotation, revocation |
| Second factor (TOTP module) | Optional per account; required to retire a resource |
| Permissions and role bundles | `member`, `steward`, `admin` — see below |
| Organizations and RLS | Every resource and reservation is org-scoped; cross-tenant reads fail in the database |
| CRUD | Resources: create, read, update, retire |
| Non-CRUD domain commands | `check-out`, `return`, `cancel` |
| Jobs | A periodic sweep marks no-shows and enqueues reminders |
| Transactional email | Reservation confirmed; starting in an hour; recorded as a no-show |
| Uploads (storage module) | A resource photo, and a return-condition photo |
| Notifications (+ SSE) | In-app "your reservation starts soon", "the resource you wanted is free" |
| Audit records | Every state transition, every resource create and retire, every role grant |
| Filtering | Reservations by resource, state, and date range |
| Pagination | Cursor pagination by start time; an offset report for utilization |
| Responsive UI and accessibility | Resource list, week calendar, reservation form, steward view — WCAG 2.2 AA |
| Deployment | The documented OCI image through the Coolify recipe |
| Backup and restore | A verified backup restored into a clean environment |
| Upgrade | `nise upgrade` run against this application, with its diff reviewed |

### Roles

Three bundles, and the boundaries between them are the reason there are three:

| Role | May |
|---|---|
| `member` | Read resources and the calendar; create, cancel, check out, and return **their own** reservations |
| `steward` | Everything a member may, plus manage **anyone's** reservation in their organization, and create and edit resources |
| `administrator` | Everything, plus invite people, grant roles, and retire a resource |
| `auditor` | Read the log, the directory, the resources, and the calendar. Change nothing. |

The interesting boundary is not between the roles but inside them: acting on
**your own** reservation needs no permission at all, because it is yours. The
`reservations.manage` permission exists solely for acting on somebody else's,
and the check in the use case is "holder **or** `reservations.manage`" rather
than a permission test. That asymmetry is what makes `steward` a real role
instead of a bigger `member`.

`auditor` is there for the property it makes testable: a role that holds no
write permission, ever, checked separately from the permission matrix so that
making the table agree with a mistake does not make the test pass.

Retiring a resource requires reauthentication, for the same reason granting a
role does: it withdraws capability from everybody at once and cannot be undone
by the person it surprises. Editing a resource's description does not — a
matrix that covers everything is one somebody turns off.

## What it deliberately is not

- **No money.** No price, no invoice, no payment, no tax, no currency. That is
  POS Kosova's territory, and deciding it here — inside the framework's own
  repository, months before the application that must live with those decisions
  exists — is precisely what Stage 6 is arranged to prevent
  ([ADR 0028](../../docs/adr/0028-reference-application.md)).
- **No recurring reservations, approval workflows, or waiting lists.** Each
  would add domain surface without adding a framework capability that is not
  already covered.
- **No calendar synchronization or mobile application.** Both are integrations,
  and an integration proves something about the integration.
- **Not a product.** It is a proof. It is MIT-licensed like everything here and
  you may take it, but nobody is maintaining it as software.

## Layout

```text
examples/reference/
  README.md          this specification
  recipe.json        the exact `nise new` arguments
  app/               the overlay: application-owned files only
  build.sh           generate a project and apply the overlay
```

`app/` contains **only files a developer wrote**. Nothing the generator owns
appears there — a file with the Nise-owned header in an overlay would mean the
reference application works by overwriting generated output, which is the one
thing [ADR 0003](../../docs/adr/0003-application-ownership.md) says an
application never needs to do. That is checked rather than intended.

## Building it

```sh
examples/reference/build.sh /tmp/workbench
cd /tmp/workbench
go mod edit -replace github.com/drilonrecica/nise-and-go=/path/to/nise-and-go
go mod tidy
pnpm --dir frontend install
nise dev
```

Building runs the same `nise new` a reader would run, from the documentation
they would read. An application that only builds from a snapshot proves that a
project was generated once; this one proves generation still works.

The `replace` is the pre-release step every generated project needs until
`github.com/drilonrecica/nise-and-go v0.1.0` reaches the module proxy. It is
documented in the generated README too, and it goes away with the first
published release.

### Deploying it

```sh
go mod vendor                       # while the framework is unpublished
pnpm --dir frontend install         # to produce the lockfile the image needs
cd deploy
POSTGRES_PASSWORD=… APP_DB_PASSWORD=… CURSOR_SIGNING_KEY="k1:$(openssl rand -base64 32)" \
  docker compose up -d --build
```

`go mod vendor` is the other half of the same pre-release step: the image
builds in a container that cannot see a local checkout, so the framework has to
travel in the build context. Drop it, and restore the `go mod download` line in
`deploy/Dockerfile`, once the module resolves from the proxy.

`APP_DB_PASSWORD` is a second, different secret. The application connects as an
unprivileged role rather than the one that owns the tables, because row-level
security does not apply to a table's owner — see
[Two database roles](../../docs/multitenancy.md) and the generated
`deploy/coolify.md`.

## Acceptance

The reference application is finished when all of the following hold. Each maps
to a task in the milestone that owns it.

1. It generates, builds, and its own `nise check` and `go test` pass.
2. Authentication, permissions, and organizations work, and cross-tenant access
   fails **in the database**, not only in the handler.
3. CRUD and both domain commands work, with their refusals tested rather than
   assumed.
4. Jobs, email, uploads, notifications, and audit records are exercised by real
   flows, against real PostgreSQL.
5. Filtering, pagination, the responsive UI, and the accessibility target are
   demonstrated and measured.
6. It deploys through the documented OCI/Coolify path and becomes ready.
7. A verified backup restores into a clean environment and the data is there.
8. The documentation is complete from a clean reader's perspective — somebody
   with no prior knowledge can follow it from install to deployed.
9. Every V0.1 acceptance gap is closed or recorded as a known limitation.

## Related

- [ADR 0028 — why this application, and why an overlay](../../docs/adr/0028-reference-application.md)
- [Generated application layout](../../docs/generated-application-layout.md)
- [Authorization](../../docs/authorization.md)
- [Multitenancy](../../docs/multitenancy.md)
- [Deployment](../../docs/deployment.md)
- [Backups](../../docs/backups.md)
