# ADR 0003: Keep Generated Application Code Application-Owned

- **Status:** Accepted
- **Date:** 2026-08-26

## Context

Business rules and user interfaces diverge quickly. Permanently framework-owned generated features would either block customization or make upgrades overwrite application work.

## Decision

Generated business features and frontend components belong to the application. Nise may fully own clearly marked infrastructure files, but it never silently overwrites handwritten application code.

Nise remains primarily build-time with a small runtime surface. Applications may use direct pgx, add dependencies, replace UI components, and eventually detach without a special eject operation.

## Consequences

- Applications can evolve normally.
- Template improvements do not automatically rewrite existing customized files.
- Upgrades require reviewable migrations or safe codemods.
- `nise check` enforces compatibility and safety invariants, not permanent starter conformity.

