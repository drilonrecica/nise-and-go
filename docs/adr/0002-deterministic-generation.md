# ADR 0002: Require Deterministic Generation

- **Status:** Accepted
- **Date:** 2026-08-26

## Context

Generators must be reviewable, testable, reproducible, and safe to use in CI. AI-generated output and remote model calls are nondeterministic and introduce privacy, availability, cost, and provenance concerns.

## Decision

Given the same Nise version and project recipe, generation produces the same files. Nise contains no AI, BYOK, model-provider, or autonomous patch functionality.

CI regenerates Nise-owned output and fails when it creates an unexpected diff.

## Consequences

- Builds and upgrades remain reproducible.
- General coding assistance is delegated to external developer tools.
- Nise may generate static documentation that helps those tools understand a project.

