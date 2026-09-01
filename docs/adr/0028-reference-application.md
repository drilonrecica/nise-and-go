# ADR 0028: The reference application is an overlay, not a committed tree

- **Status:** Accepted
- **Date:** 2026-09-01

## Context

V0.1 requires a compact, realistic multi-user business application that
exercises the whole surface end to end: authentication, permissions,
organizations, CRUD, a non-CRUD domain command, jobs, transactional email,
uploads, notifications, audit records, filtering, pagination, deployment,
backup, and upgrade. `examples/reference/` has been reserved for it since the
repository layout was written.

Two questions had to be answered before any of it could be built: what the
application *is*, and what of it lives in this repository.

### What it is

The blueprint rules out a to-do list (too small to prove anything) and POS
Kosova (the first real application, deliberately built *after* the V0.1 freeze
so that its needs cannot shape the framework in advance).

That second exclusion is stronger than it first reads. If the reference
application were an invoicing or point-of-sale system, every money decision it
needed — minor units, rounding, tax, currency — would be made inside the
framework's own repository, months before the real application that has to live
with them. Stage 6 exists precisely to stop that. The reference application
should therefore be structurally demanding and **domain-distant** from POS.

### What lives here

The obvious option is to commit the generated tree: `nise new`, then edit, then
`git add`.

It has a real cost. A generated project is about 250 files, nearly all of them
byte-identical to `templates/`, and committing them puts a second copy of the
templates in the repository — one that no `make generate-diff` gate keeps
correct, and that reviewers scroll past. Worse, it makes the interesting part —
the handful of files a developer actually wrote — invisible among the files
they did not.

## Decision

**The reference application is a shared-equipment reservation and checkout
system.** Organizations own bookable resources; members reserve them for a time
window; a reservation is checked out and returned. It is `examples/reference/`,
and it is named `workbench`.

It is chosen for what it demands rather than for what it is:

| Requirement | How this domain demands it |
|---|---|
| Organizations and RLS | A resource belongs to one organization; a reservation is visible only inside it |
| Permissions and role bundles | Reserving for yourself, managing somebody else's reservation, and retiring a resource are three different rights |
| A non-CRUD domain command | `check-out` and `return` are state transitions with preconditions, not field updates |
| Correctness in the database | Double-booking is prevented by an exclusion constraint, not by application code that read first |
| Concurrency worth testing | Two people reserving the same instrument at the same moment is the natural adversarial test |
| Jobs | A periodic sweep marks no-shows and enqueues reminders |
| Email, notifications, uploads, audit | Confirmations, "starts in an hour", a resource photo and a return-condition photo, and every transition recorded |
| Filtering and pagination | Reservations by resource, state, and date range, cursor-paginated by start time |

And **no money**. There is no price, no invoice, and no payment anywhere in it.
That is the point of the choice, not an omission.

**What lives in this repository is the overlay: the application-owned files
only.** `examples/reference/` holds the recipe (`nise new` arguments), the
hand-written source, and a script that produces the running application by
generating a project and applying the overlay to it.

## Consequences

- **The diff is the application.** Every file under `examples/reference/app/`
  is a file a developer wrote. Reviewing the reference application means
  reading those files, not finding them.
- **It is testable in a way a snapshot is not.** A committed tree proves that a
  project was generated once. An overlay proves that generation *still* works,
  that the ownership rules hold, and that the same commands in the
  documentation produce the same application — because building it runs them.
- **The overlay may never contain a Nise-owned path.** A file the generator
  owns appearing in the overlay would mean the reference application works by
  overwriting generated output, which is the exact thing ADR 0003 forbids
  applications from needing to do. This is checked, not merely intended.
- **It cannot be browsed as a whole on GitHub.** Somebody wanting to read the
  complete tree has to build it. The build is one command, and the part they
  probably wanted is the part that is visible.
- **It is not in `GO_PACKAGES`.** The overlay is Go source for a *different*
  module — it imports `workbench/internal/...` — so this repository's `go vet`,
  `golangci-lint`, and `go test` cannot compile it. It is checked by building
  the application and running *its* checks, which is what a real application
  does anyway.
- **POS Kosova stays the first application whose needs shape the framework.**
  Nothing about billing, tax, or currency is decided here.

## Alternatives considered

**Commit the generated tree.** Rejected above: a second, ungated copy of
`templates/` that hides the application inside it.

**Keep the reference application in a separate repository.** It would be a
truer test — an application developed outside the Nise repository is what V1.0
requires an upgrade guide to work on. It was rejected *for V0.1* because the
V0.1 gate is "does the whole surface work", and splitting it across two
repositories adds a synchronization problem to a question that does not need
one. The separate-repository version is the right shape for the V1.0 upgrade
requirement, and is where the second application should live.

**A to-do list or a blog.** Rejected by the blueprint, and rightly: neither has
a state transition, a concurrency hazard, or a reason for two roles to exist.
An application that cannot fail interestingly proves nothing about a framework
whose value is in the failure paths.

**An invoicing system.** Rejected for the reason in Context: it would make the
framework's money decisions before the application that has to live with them
exists.
