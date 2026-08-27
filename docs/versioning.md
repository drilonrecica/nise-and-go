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

`nise doctor` detects incompatible combinations. `nise upgrade` updates versioned runtime dependencies where safe and presents source migrations as reviewable changes.

## Upgrade rules

- Safe mechanical codemods are allowed.
- Handwritten application code is never silently replaced.
- Database migrations remain explicit.
- Package-manager-owned CLI binaries are updated through their package manager.
- Applications may remain on an older supported version or detach from Nise.

## Requirements for 1.0

- Two real production applications.
- One genuine application upgrade cycle.
- A documented threat model and security baseline.
- Repeatable performance baselines.
- Tested backup and restoration.
- An upgrade guide proven outside the Nise repository.

