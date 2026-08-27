# Performance Baselines

**Status:** Initial baseline measurements (v0.0.0-dev). These are tracked measurements, not hard budgets. The regression review policy is: any deterministic increase above 10% requires explanation.

## Environment

- **Commit:** 8f69bd8149f8d624c1ac4fdd2801d635a2de5ef6
- **Build date:** 2026-08-28
- **Go version:** go1.26.5-X:nodwarf5
- **GOOS/GOARCH:** linux/amd64
- **CPU:** AMD Ryzen 7 (16 cores)
- **RAM:** 32.0 GB
- **Kernel:** 7.1.5-200.fc44.x86_64
- **OS:** Linux
- **Note:** Single-machine measurement on uncontrolled hardware; variance expected across different systems and conditions

## Measurement methodology

All measurements run locally in temporary directories under `/tmp`, clean up after themselves, and use the portable `scripts/measure-baselines.sh` script. Each metric reports five or more samples unless otherwise noted.

**Generated application setup:** The script generates a project using `nise new`, then builds the application. Because `github.com/drilonrecica/nise-and-go v0.1.0` is not yet published on the module proxy, the script runs `go mod edit -replace github.com/drilonrecica/nise-and-go=/path/to/nise-and-go` (pointing to the local repository) before `go mod tidy`. This is necessary for the build to succeed and is part of the baseline methodology; it does not affect the measurements themselves (only the application binary's size and startup behavior, which are what we measure).

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

Three independent runs, five samples each:

| Run | Samples (ms) | Median (ms) |
|-----|---|---|
| 1   | 9.8, 12.0, 32.2, 37.7, 37.9 | 32.2 |
| 2   | 12.7, 36.6, 38.0, 10.9, 15.6 | 15.6 |
| 3   | 9.1, 10.9, 34.7 | 10.9 |

- **Observed range:** 9.1–38.0 ms
- **Across-run median spread:** 10.9–32.2 ms (Δ = 21.3 ms)

**Variance note:** A bimodal pattern persists across all three runs—some samples complete in ~10 ms, others in 30+ ms—but the position of fast vs. slow samples varies. This is not positional (not "later samples are always slow") and post-measurement testing ruled out leftover processes (verified via `ps` and `ss`) and DNS overhead (curls use literal IP 127.0.0.1, no name resolution). The residual cause is unidentified, likely host scheduling jitter on uncontrolled hardware. This range requires a controlled or dedicated benchmark runner to resolve ([PERFORMANCE_BUDGETS.md](../privateDocs/PERFORMANCE_BUDGETS.md#principles)); the 10% regression threshold (below) applies loosely to a metric this noisy.

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

## Baseline stability and regression review policy

**Stable metrics** (< 10% variance across runs):
- `nise` CLI binary size (unstripped/stripped)
- `nise version` warm latency
- `nise new` duration
- Generated app binary size
- Idle RSS

These are reliable baselines. **Any increase above 10% requires explanation** in commit messages or ADRs.

**Noisy metric** (> 50% variance across runs):
- Generated app cold startup (9.1–38.0 ms)

This metric requires a controlled benchmark runner to stabilize (see [PERFORMANCE_BUDGETS.md](../privateDocs/PERFORMANCE_BUDGETS.md#principles) for the path forward). The 10% threshold is less meaningful here; compare against the range you measured, not a single point.

## How to regenerate

```bash
./scripts/measure-baselines.sh --output-dir docs --samples 5
```

The script produces:

- `docs/baseline-metrics.json` — machine-readable NDJSON output for CI/CD tooling.
- Updated human-readable summary in this file (manual merge required for now).

The script re-runs the entire measurement cycle: it does not assume a warm cache and does not persist state across invocations, so results are independent and reproducible (modulo uncontrolled hardware variance).

## Rationale

**Reliable baselines track:**

- **CLI responsiveness:** `nise version` completes in ~2.9 ms; `nise new` in ~5.0 ms. These are stable and prevent regression.
- **Generation throughput:** Project creation speed remains consistent as features are added.
- **Application size and memory:** The generated application's binary (10.0 MB) and idle memory footprint (8.9–9.1 MB) are stable, load-bearing for deployment.

**Measured but noisy:**

- **Application cold startup:** Observed 9.1–38.0 ms across independent runs. Real variance, not instrumentation noise. Awaits a controlled benchmark runner to establish stable baselines (see [PERFORMANCE_BUDGETS.md](../privateDocs/PERFORMANCE_BUDGETS.md#principles)).

They do not measure:

- HTTP latency or throughput (reference workloads not yet defined).
- Memory usage under load (requires controlled benchmark harness).
- Frontend performance (Svelte build and runtime metrics separate, tracked by frontend CI).
- Database query performance (depends on schema and workload).
