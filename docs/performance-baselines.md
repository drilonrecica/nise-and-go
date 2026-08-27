# Performance Baselines

**Status:** Initial baseline measurements (v0.0.0-dev). These are tracked measurements, not hard budgets. The regression review policy is: any deterministic increase above 10% requires explanation.

## Environment

- **Commit:** 72688ad45816cb5e2a2b1d26a3dcc7a87cec976e
- **Build date:** 2026-08-27
- **Go version:** go1.26.5-X:nodwarf5
- **GOOS/GOARCH:** linux/amd64
- **Hardware:** Single-machine measurement (uncontrolled hardware; variance expected)

## Measurement methodology

All measurements run locally in temporary directories under `/tmp`, clean up after themselves, and use the portable `scripts/measure-baselines.sh` script. Each metric reports five or more samples unless otherwise noted.

### CLI measurements

**nise CLI binary size:**

- Unstripped: 6,499,379 bytes (6.2 MB)
- Stripped: 4,375,672 bytes (4.2 MB)

Reduction with `strip`: ~33%.

**nise version warm latency:**

Measured `nise version` execution time after a warm process start. Five samples:

| Sample | Duration (ms) |
|--------|---------------|
| 1      | 2.870         |
| 2      | 3.095         |
| 3      | 2.837         |
| 4      | 2.939         |
| 5      | 3.210         |

- **Median:** 2.939 ms
- **Spread:** 2.837–3.210 ms (Δ = 0.373 ms, ~13% variance)

**nise new duration:**

Measured wall-clock time from process start to project creation completion (37 files written). Five samples:

| Sample | Duration (ms) |
|--------|---------------|
| 1      | 5.177         |
| 2      | 5.178         |
| 3      | 5.051         |
| 4      | 5.032         |
| 5      | 5.028         |

- **Median:** 5.051 ms
- **Spread:** 5.028–5.178 ms (Δ = 0.150 ms, ~3% variance)

### Generated application measurements

These measurements run inside a freshly generated project (profile: go-chi-postgres-svelte).

**Generated application binary size:**

- Size: 10,020,375 bytes (9.6 MB)

This is the unstripped `./cmd/testapp/testapp` binary for the default profile.

**Generated application startup (cold):**

Measured via `./cmd/testapp/testapp --help` invocation. This approximates binary load and initialization without requiring a full database or server setup.

Five samples:

| Sample | Duration (ms) |
|--------|---------------|
| 1      | 4.525         |
| 2      | 4.662         |
| 3      | 4.721         |
| 4      | 4.509         |
| 5      | 4.343         |

- **Median:** 4.525 ms
- **Spread:** 4.343–4.721 ms (Δ = 0.378 ms, ~8% variance)

**Measurement limitation:** This measures only process startup and binary initialization. Full server startup to a passing readiness probe would require database connectivity and schema setup, which the portable measurement script deliberately avoids (see "No network, no telemetry" in [constraints.md](constraints.md)). A future controlled-runner benchmark will measure true cold startup including migrations.

## Regression review policy

Until stable baselines with variance bounds are established:

- Any **deterministic increase above 10%** in a metric requires explanation in commit messages or ADRs.
- Variance measurement remains manual until a controlled runner is in place.
- For noisy wall-clock metrics measured on uncontrolled hardware, "deterministic" means consistent across at least three independent runs (this script produces five samples per run).

## How to regenerate

```bash
./scripts/measure-baselines.sh --output-dir docs --samples 5
```

The script produces:

- `docs/baseline-metrics.json` — machine-readable NDJSON output for CI/CD tooling.
- Updated human-readable summary in this file (manual merge required for now).

The script re-runs the entire measurement cycle: it does not assume a warm cache and does not persist state across invocations, so results are independent and reproducible (modulo uncontrolled hardware variance).

## Rationale

These measurements track:

- **CLI responsiveness:** `nise` users see it as instant today; this baseline prevents regression.
- **Generation throughput:** Project creation speed remains consistent as features are added.
- **Application baseline:** The generated application's binary size and startup time are load-bearing for deployment and user experience.

They do not measure:

- HTTP latency or throughput (reference workloads not yet defined).
- Memory usage under load (requires controlled benchmark harness).
- Frontend performance (Svelte build and runtime metrics separate, tracked by frontend CI).
- Database query performance (depends on schema and workload).
