# internal/generator

Deterministic generation internals: template rendering, file ownership rules, and regeneration-diff logic.

Private to this module. Generation must be byte-for-byte stable across runs and environments, and must refuse to overwrite application-owned files (ADR 0002, ADR 0003).

Populated by Slice 1 (`nise new`) and Slice 2 (`nise generate`).
