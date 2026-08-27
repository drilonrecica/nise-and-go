# Performance Baselines

**Status:** Initial baseline measurements (v0.0.0-dev). These are tracked measurements, not hard budgets. The regression review policy is: any deterministic increase above 10% requires explanation.

## Environment

- **Commit:** 8f69bd8149f8d624c1ac4fdd2801d635a2de5ef6
- **Build date:** 2026-08-28
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
| 1      | 3.069         |
| 2      | 2.837         |
| 3      | 2.776         |
| 4      | 2.923         |
| 5      | 2.873         |

- **Median:** 2.873 ms
- **Spread:** 2.776–3.069 ms (Δ = 0.293 ms, ~10% variance)

**nise new duration:**

Measured wall-clock time from process start to project creation completion (37 files written). Five samples:

| Sample | Duration (ms) |
|--------|---------------|
| 1      | 5.073         |
| 2      | 5.011         |
| 3      | 5.037         |
| 4      | 5.014         |
| 5      | 5.043         |

- **Median:** 5.037 ms
- **Spread:** 5.011–5.073 ms (Δ = 0.062 ms, ~1% variance)

### Generated application measurements

These measurements run inside a freshly generated project (profile: go-chi-postgres-svelte).

**Generated application binary size:**

- Size: 10,020,375 bytes (9.6 MB)

This is the unstripped `./cmd/testapp/testapp` binary for the default profile.

**Generated application startup (cold):**

Measured by starting the generated binary and polling `/healthz/ready` until it returns HTTP 200. Excludes any database migration time (none exists in this slice; M3 and later introduce persistent state).

Five samples:

| Sample | Duration (ms) |
|--------|---------------|
| 1      | 9.276         |
| 2      | 14.062        |
| 3      | 9.703         |
| 4      | 9.620         |
| 5      | 11.804        |

- **Median:** 9.703 ms
- **Spread:** 9.276–14.062 ms (Δ = 4.786 ms, ~49% variance)

Variance is higher here than other metrics because process startup involves system calls whose timing varies on uncontrolled hardware.

**Generated application idle RSS:**

Measured by reading `VmRSS` from `/proc/<pid>/status` after the server settles on the readiness probe for at least 100ms. This is the process memory footprint only, not including any database connection pool or cache.

Five samples:

| Sample | RSS (MB) |
|--------|----------|
| 1      | 8.8      |
| 2      | 9.0      |
| 3      | 8.8      |
| 4      | 9.1      |
| 5      | 8.9      |

- **Median:** 8.9 MB
- **Spread:** 8.8–9.1 MB (Δ = 0.3 MB, ~3% variance)

No database process is running (this slice has no M3 database layer). RSS is measured for the application process alone. A future controlled benchmark will measure full-stack memory (application + PostgreSQL).

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
