# Versioning and Compatibility

This page describes planned policy. Tags use semantic-versioning format from the first release; compatibility guarantees begin at `1.0`.

## V0.x

V0.x is explicitly unstable. Breaking changes are permitted, but each release must include:

- A clear breaking-change entry.
- Required application migrations.
- Generator and runtime compatibility information.
- Any database or rollback limitations.

## V1.0 and later

After `1.0`, documented public runtime APIs follow semantic versioning. Generator internals remain private. Application-owned generated files are not silently overwritten to force conformity with a new template.

## Project metadata

Each project recipe records:

- The Nise CLI version that created or last upgraded the project.
- Runtime-package versions.
- Selected profile and compile-time modules.

`nise doctor` detects incompatible combinations, and
[`nise upgrade`](commands/upgrade.md) moves a project forward: it regenerates
Nise-owned files, bumps every dependency nise pins in `go.mod` and the
generated `frontend/package.json`, applies the exact textual codemods a
migration declares over application-owned Go source, and prints everything else
for you to do by hand.

## Upgrade rules

- Safe mechanical codemods are allowed. A codemod is an **exact textual
  replacement a migration declares** — never an AST rewrite, never a
  reformatting pass — and every application is counted and reported with its
  file. `--no-codemods` refuses them and turns each into a note.
- Handwritten application code is never silently replaced. `nise upgrade`
  regenerates only files carrying the Nise-owned header; a change to an
  application-owned file is printed as a note for you to make.
- A dependency you removed stays removed, and one you pinned ahead of nise
  stays ahead and is named. An upgrade only ever moves a version forward.
- Database migrations remain explicit. `nise upgrade` does not run one.
- Package-manager-owned CLI binaries are updated through their package
  manager. [`nise version check`](commands/version.md) reports which command
  that is for the way you installed nise, and runs none of them.
- Applications may remain on an older supported version or detach from Nise.

An upgrade performs no network access. `go mod tidy` and `pnpm install` are
named in its output rather than run, because an upgrade must not be the command
that discovers a dependency is unreachable — least of all halfway through
writing files.

## Requirements for 1.0

- Two real production applications.
- One genuine application upgrade cycle.
- A documented threat model and security baseline.
- Repeatable performance baselines.
- Tested backup and restoration.
- An upgrade guide proven outside the Nise repository.

