# Project Status

## Current phase

Nise & Go is in design and pre-alpha development. No supported production release exists.

The V0.1 target is frozen around one golden profile:

- Go and chi.
- PostgreSQL, pgx, sqlc, and Goose.
- SvelteKit with Svelte 5 runes.
- OpenAPI-generated server and frontend boundaries.
- One binary containing the API, worker, migrations, and static authenticated frontend.

## What is proved, and how

The V0.1 acceptance criteria are the ones in this project's own implementation
plan, one group per vertical slice. Each row below names the evidence rather
than asserting the property, because an unevidenced checklist is a checklist
that eventually lies.

### Generated project boots

| Criterion | Evidence |
|---|---|
| A clean machine can generate and run it | [The tutorial](tutorial.md), followed from an empty directory: `doctor` 4 ok / 0 fail, `nise new` 249 files, `check` 7 ok / 0 fail, both test suites passing |
| Generation twice produces no diff | `test/golden`, and `diff -r` between two runs |
| Non-interactive output has no animation | Zero ANSI escapes in piped output |
| No implicit telemetry or update traffic | `test/nonetwork`, five proofs — [no telemetry](no-telemetry.md) |

### Database-backed resource

| Criterion | Evidence |
|---|---|
| Transaction ownership is visible | Use cases own their transactions; [transactions](database-transactions.md) |
| Malformed and unknown input fails strictly | `httpjson` suite, `FuzzDecode`, `FuzzTransportSurface` |
| List state is in the URL | The reference application's list pages, and its end-to-end journeys |
| Editing an app-owned file is never overwritten | `nise check`'s ownership invariant; verified by editing one and regenerating |

### Security boundary

| Criterion | Evidence |
|---|---|
| Default deny is demonstrated | `authorization` suite; the permission matrix checked in both directions |
| Cookie and origin behaviour is tested | `httpauth` suite; a live cross-site POST answers `403 cross_site_request` |
| Rotation, revocation, and Argon2id rehash work | Generated `auth` suites against real PostgreSQL |
| Logs carry no credential or session value | `runtime/logging` redaction tests; a live run logs `"session_cookie":"[REDACTED]"` |

### Asynchronous services

| Criterion | Evidence |
|---|---|
| A rolled-back transaction leaves no job | Checked against the real `river_job` table, not a fake |
| Retry and terminal failure are deterministic | Generated `jobs` suites |
| Upload authorization and finalization are server-controlled | Generated `uploads` suite; the reference application's photo flow |
| Polling works when SSE is unavailable | **Not met.** See below. |

### Optional organizations

| Criterion | Evidence |
|---|---|
| Cross-tenant read and write fail | Under a `NOSUPERUSER NOBYPASSRLS` role: no tenant reads nothing, the wrong tenant reads nothing, a cross-tenant write is refused |
| Pool reuse cannot retain tenant context | Eight tenants over a two-connection pool |
| Jobs establish explicit tenant context | Each job carries its own tenant |
| Administrative bypass is narrow and audited | There is **no** administrative bypass: no code path sets a tenant it was not given, and production refuses to start as a role that can bypass row-level security |

### Release and operations

| Criterion | Evidence |
|---|---|
| Clean deployment succeeds | The reference application deployed through `deploy/compose.yaml`: 14 migrations, all four modules, three probes at 200, RLS forced |
| Rollback rehearsal succeeds | **Locally only.** See below. |
| Backup restores into an empty environment | Restored into a database with zero tables: rows, schema, both constraints and RLS all present, and the restored database refuses an overlapping reservation |
| Every installation path reports the same version | Proved locally for two channels; a defect found and fixed doing so. **Never run against a published release.** |

## What is not finished

Stated plainly, because a gap nobody wrote down is a gap somebody discovers.

- **Nothing has ever been released.** Every claim about release artifacts,
  installation channels, the Homebrew tap, and rollback is proved by tests and
  by local builds, and none of it has run against a published release. The
  workflows that would prove it exist and have never fired.
- **Three optional modules have no HTTP surface.** `uploads`, `notifications`,
  and TOTP enrolment ship complete use cases, tables, workers, and tests, and a
  browser cannot reach any of them: there are no paths for them in the
  generated OpenAPI document. An application selecting one of those modules
  gets working server-side machinery and has to write its own endpoints. This
  is the largest gap on this page.
- **A person cannot discover which organizations they belong to.** Entering a
  named tenant works and is enforced; listing your own memberships would be a
  read across tenants, and `organization_members` is itself tenant-scoped.
- **The reference application is a slice, not a product.** Resources and
  reservations work end to end — list, book, check out, return, cancel, filter,
  paginate — and there are no create or edit forms, no calendar view, and no
  organization switcher.
- **`nise upgrade` has never performed a real upgrade**, because there has
  been no earlier version to upgrade from. Its migration table is empty and
  its machinery is exercised against fixtures.
- **CI does not run the reference application's database-backed suites.** It
  builds and vets it on every push; the PostgreSQL-backed tests are run by
  hand.

## What “frozen” means

V0.1 may implement and correct the documented foundation. It may not add
another router, database, frontend framework, plugin system, AI integration,
billing system, or deployment platform merely because it could be useful
someday.

An addition must either:

1. Be required for a documented V0.1 capability to work safely; or
2. Replace an existing scope item.

The gaps above are measured against that rule rather than filed as future
work. The module HTTP surfaces are the case where it bites: a module whose
server side works and whose client side does not exist is a capability that
does not work safely, so completing it qualifies under (1) — and doing so is
still an addition, made deliberately, rather than a feature that arrived
because somebody had the afternoon.

## Validation path

1. Implement the deterministic CLI and golden-profile foundation.
2. Prove the complete surface through a realistic reference business application.
3. Freeze framework features.
4. Build POS Kosova against Nise.
5. Correct deficiencies proven by the real application.
6. Validate with a smaller second application.
7. Complete a real upgrade cycle before `1.0`.

## Unsupported claims

Until evidence exists, Nise must not claim to be:

- Production ready.
- Audited.
- The fastest Go framework.
- Maximally secure.
- Backward compatible.
- Suitable for a particular compliance regime.
