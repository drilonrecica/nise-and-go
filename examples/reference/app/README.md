<!-- Generated once by nise; owned by this application. Nise will not overwrite it. -->

# Workbench

Shared equipment, reserved and checked out.

An organization owns bookable **resources** — instruments, machines, cameras,
rooms. Its members make **reservations** for a time window, **check out** the
resource when the window arrives, and **return** it when they are done.

This is the reference application for
[Nise & Go](https://github.com/drilonrecica/nise-and-go). It exists to prove
the framework's whole surface works together in one application, with nothing
mocked. It is not a product and nobody is maintaining it as software.

The specification, the reasoning behind the domain, and what it deliberately
does not do are in the framework repository, under `examples/reference/`.

## The shape of it

```
                  cancel
      ┌──────────────────────────────┐
      │                              ▼
   booked ──check-out──▶ checked_out ──return──▶ returned
      │
      └──(window ended, never collected)──▶ no_show
```

`check-out` and `return` are domain commands rather than field updates. Each
has preconditions, writes an audit record, and is idempotent.

Two people cannot reserve one resource for overlapping windows, and that is
enforced by an exclusion constraint in PostgreSQL rather than by a read before
a write — a read-then-write check passes for both under concurrency, which is
exactly when it matters.

## Roles

| Role | May |
|---|---|
| `member` | Read resources; create, cancel, check out, and return **their own** reservations |
| `steward` | The above, plus manage anyone's reservation in their organization, and create and edit resources |
| `admin` | The above, plus invite people, grant roles, and retire a resource |

Retiring a resource requires reauthentication, because it withdraws capability
from everybody at once.

## Running it

```sh
go mod tidy
pnpm --dir frontend install
nise dev
```

`nise dev` starts PostgreSQL in a container, applies migrations, and runs the
Go server and the Vite dev server behind one origin. Everything else — checks,
tests, migrations, backups — is the standard Nise application workflow:

```sh
nise check
nise test
nise db status
```

## Layout

This is an ordinary Nise application, so its layout is the documented one. The
parts that are specific to Workbench:

| Path | Holds |
|---|---|
| `internal/features/resource/` | Bookable resources: CRUD, retirement, photos |
| `internal/features/reservation/` | Reservations, the state machine, and the two domain commands |
| `db/migrations/` | The schema, including the exclusion constraint that prevents double-booking |
| `frontend/src/routes/(app)/resources/` | Resource list, detail, and the week calendar |
| `frontend/src/routes/(app)/reservations/` | My reservations, and the steward view |

## Licence

MIT, like the framework.
