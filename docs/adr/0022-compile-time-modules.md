# 0022: A compile-time module is a framework package plus conditional templates

- **Status:** Accepted
- **Date:** 2026-08-31

## Context

The blueprint has always said optional capabilities are compile-time modules with explicit generated wiring, no dynamic loading, and no automatic discovery. Until now that was a sentence and an empty `modules/` directory: the recipe could record a selection, and `SelectedModules()` returned the names, but nothing was contributed by selecting one.

The optional TOTP module is the first real one, so the mechanism has to be decided rather than improvised — and decided once, because three more modules follow it.

## Decision

A module is two things, and nothing else.

### 1. A Go package under `modules/`

`modules/totp` holds the primitives: producing and checking codes, generating recovery codes. It is ordinary Go in the framework's own module, imported by a generated application exactly like a `runtime/` package.

It is not in `runtime/` because [ADR 0011](0011-runtime-public-api.md) fixes that package list as the surface every generated application gets and every application may rely on. A module's package is used only by applications that selected it, and its stability policy follows the module rather than the runtime: during `0.x` both are unstable, and at `1.0` a module package gets the same compatibility promise as `runtime/` for as long as the module exists.

The dependency allowlist governs `modules/` exactly as it governs `runtime/`.

### 2. Template files that render only when the module is selected

Each manifest entry carries an optional module name. An entry with one renders only when the recipe selected that module; an entry without one renders always.

**A module that is not selected contributes nothing** — not an empty file, not a stub, not a build-tagged placeholder, not an unused import. A generator test asserts that a project generated without the module contains no file mentioning it. That is what makes "compile-time" honest: the absent module is absent from the tree, from the binary, and from the reader's attention.

### Wiring is application-owned Go, not a registry

The selection lives in `internal/app/modules.gen.go`, which is Nise-owned and regenerated. The **constructor calls** live beside it in application-owned files — `internal/app/secondfactor.go` for this module — written once and yours thereafter.

The split is forced by an invariant worth keeping: `internal/check` re-plans a project to detect drift in Nise-owned files, and it does so without knowing the project's real name or module path. A Nise-owned file that imported `<module path>/internal/features/mfa` would break that, and every renamed directory would start reporting phantom differences. So `modules.gen.go` has no imports at all.

The wiring function's signature does not depend on the selection, so the call sites in `app.go` and `enroll.go` do not branch. What changes is what it returns.

### Absence is declared, not defaulted

An application generated without the TOTP module returns `auth.NoSecondFactor` — a named type whose methods say there is no second step — and the login use case *requires* a `SecondFactor`, refusing a nil. The alternative, an optional dependency defaulting to nil, makes "this application has no second factor" indistinguishable from "somebody forgot to wire it".

### Module migrations are numbered contiguously after the core history

The runtime's migration compatibility check requires a history with no gaps, because a gap is how a missing migration hides. Module migrations therefore continue the core numbering — the TOTP module's is `00009_totp.sql` in a project whose core history ends at `00008` — assigned in module order at generation time from the selection recorded in the recipe.

The number is fixed for the life of the project. A migration file is application-owned and, once applied, immutable; adding a module to an existing project means writing the next forward migration, never renumbering one already applied.

### A module may extend a closed declaration

The TOTP module adds `second_factor.disable` to the [reauthentication matrix](0021-reauthentication-matrix.md), on that ADR's own principle: removing a second factor widens what a stolen session can do. The matrix's generator test runs over both the default and the every-module plan, so a module extending a closed list is still checked against the code that consumes it.

## Alternatives considered

- **Ship every module's code always, gated by configuration.** Rejected: it makes "optional" mean "present but off", which is a larger attack surface, a larger binary, and a switch somebody can flip by accident.
- **A plugin interface with registration.** Rejected by the blueprint, and rightly: registration through `init()` is discovery by another name, and a reader cannot see what is wired by reading the wiring.
- **Put module primitives in `runtime/`.** Rejected: it would grow the surface every application is promised, in exchange for code most of them do not use.
- **Generate the module's constructor calls into a Nise-owned file.** Rejected: it breaks the name- and path-independence that licenses the regeneration check.
- **Number module migrations in per-module blocks (00100, 00200, …).** Rejected: it leaves gaps, and the compatibility check refuses a history with gaps — for a good reason that this convenience does not outweigh.

## Consequences

- Adding a module means: a package under `modules/`, template files with a module name in the manifest, an application-owned wiring file, and a migration number that continues the history.
- A project cannot gain a module by editing `nise.json`: the files are not there. Adding one to an existing project is an upgrade that writes files and shows the wiring change for review, which is the behavior the blueprint already describes for required application-code changes.
- The three remaining modules — organizations, notifications, uploads — have a pattern to follow and a generator test that will hold them to it.
