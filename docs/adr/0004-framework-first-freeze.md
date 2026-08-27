# ADR 0004: Build the Framework Before the First Production Application

- **Status:** Accepted
- **Date:** 2026-08-27

## Context

The maintainer's next production application (POS Kosova) could be built either on a partially finished Nise or after Nise reaches a defined V0.1. Building the application first tends to bake application-specific assumptions into the framework; building the framework first risks months of work with no real user. This is the highest-risk decision in the project.

## Decision

Nise V0.1 is completed before POS Kosova starts. "Complete" means the frozen golden-profile checklist works end to end in the reference application, not that every conceivable feature exists.

When the checklist passes, feature development freezes. Any proposed V0.1 addition must replace an existing scope item or prove that an application cannot ship safely without it. After the freeze, changes are limited to security defects, data-integrity defects, performance regressions on representative workloads, missing escape hatches, and repetition proven across multiple real features.

## Consequences

- The frozen checklist and freeze rule are the containment mechanism for the risk; they are not optional.
- The reference application, not POS Kosova, proves V0.1.
- Generalizations wait for a second concrete use case.
- Revisit only if the reference application cannot exercise a capability that POS Kosova demonstrably needs.
