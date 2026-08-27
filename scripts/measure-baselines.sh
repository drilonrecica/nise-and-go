#!/bin/bash
set -euo pipefail

# measure-baselines.sh — Performance baseline measurements for nise
#
# Usage: ./scripts/measure-baselines.sh [--output-dir /path] [--samples N]
#
# Produces both human-readable and machine-readable (JSON) output.
# All builds and measurement work happens in a temporary directory;
# nothing is persisted inside the nise repository.

# Force POSIX locale for numeric calculations
export LC_ALL=C
export LANG=C

output_dir="."
samples=5

while [[ $# -gt 0 ]]; do
    case "$1" in
        --output-dir)
            output_dir="$2"
            shift 2
            ;;
        --samples)
            samples="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
done

# Resolve repo root
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Create temporary workspace
work_dir=$(mktemp -d)
trap "rm -rf '$work_dir'" EXIT

echo "Measuring baselines in temporary workspace: $work_dir"
echo ""

# ============================================================================
# PART 1: Environment metadata
# ============================================================================

commit=$(cd "$repo_root" && git rev-parse HEAD)
version=$(cd "$repo_root" && git describe --tags 2>/dev/null || echo "v0.0.0-dev")

echo "=== Environment ==="
echo "Commit:        $commit"
echo "Version:       $version"
echo "Go version:    $(go version | awk '{print $3}')"
echo "GOOS:          ${GOOS:-$(go env GOOS)}"
echo "GOARCH:        ${GOARCH:-$(go env GOARCH)}"
echo "Samples:       $samples"
echo ""

# ============================================================================
# PART 2: Build nise CLI
# ============================================================================

echo "=== Building nise CLI ==="
nise_bin="$work_dir/nise"
cd "$repo_root"
go build -o "$nise_bin" ./cmd/nise
echo "Built: $nise_bin"

# Binary sizes
nise_size_unstripped=$(stat -c%s "$nise_bin" 2>/dev/null || wc -c < "$nise_bin")
nise_bin_stripped="$work_dir/nise-stripped"
cp "$nise_bin" "$nise_bin_stripped"
strip "$nise_bin_stripped" 2>/dev/null || true
nise_size_stripped=$(stat -c%s "$nise_bin_stripped" 2>/dev/null || wc -c < "$nise_bin_stripped")

echo "Binary size (unstripped): $nise_size_unstripped bytes"
echo "Binary size (stripped):   $nise_size_stripped bytes"
echo ""

# ============================================================================
# PART 3: Measure nise version latency (warm)
# ============================================================================

echo "=== Measuring 'nise version' latency ==="
version_latencies=()
for i in $(seq 1 "$samples"); do
    start_ns=$(date +%s%N)
    "$nise_bin" version > /dev/null 2>&1 || true
    end_ns=$(date +%s%N)
    latency_ms=$(awk "BEGIN {printf \"%.3f\", ($end_ns - $start_ns) / 1000000}")
    version_latencies+=("$latency_ms")
    printf "  Sample $i: %.3f ms\n" "$latency_ms"
done

version_latency_median=$(printf '%s\n' "${version_latencies[@]}" | sort -n | awk '{a[NR]=$1} END {if (NR % 2 == 1) print a[(NR+1)/2]; else print (a[NR/2] + a[NR/2+1])/2}')
echo "Median: $version_latency_median ms"
echo ""

# ============================================================================
# PART 4: Measure nise new duration
# ============================================================================

echo "=== Measuring 'nise new' duration ==="
new_durations=()
new_dir="$work_dir/new-projects"
mkdir -p "$new_dir"

for i in $(seq 1 "$samples"); do
    proj_dir="$new_dir/proj$i"
    rm -rf "$proj_dir"

    start_ns=$(date +%s%N)
    cd "$new_dir"
    "$nise_bin" new "proj$i" --yes > /dev/null 2>&1
    end_ns=$(date +%s%N)

    duration_ms=$(awk "BEGIN {printf \"%.3f\", ($end_ns - $start_ns) / 1000000}")
    new_durations+=("$duration_ms")
    printf "  Sample $i: %.3f ms\n" "$duration_ms"
done

new_median=$(printf '%s\n' "${new_durations[@]}" | sort -n | awk '{a[NR]=$1} END {if (NR % 2 == 1) print a[(NR+1)/2]; else print (a[NR/2] + a[NR/2+1])/2}')
new_min=$(printf '%s\n' "${new_durations[@]}" | sort -n | head -1)
new_max=$(printf '%s\n' "${new_durations[@]}" | sort -n | tail -1)
echo "Median: $new_median ms (min: $new_min, max: $new_max)"
echo ""

# ============================================================================
# PART 5: Create and measure generated project
# ============================================================================

echo "=== Generating application project ==="
gen_project_dir="$work_dir/testapp"
cd "$work_dir"
"$nise_bin" new testapp --yes > /dev/null 2>&1

if [[ ! -f "$gen_project_dir/nise.json" ]]; then
    echo "ERROR: Generated project missing nise.json" >&2
    exit 1
fi
echo "Generated project: $gen_project_dir"
echo ""

# ============================================================================
# PART 6: Build generated app
# ============================================================================

echo "=== Building generated app ==="
cd "$gen_project_dir"

# Write go.mod replace directive (required since v0.1.0 is not published)
if ! grep -q "replace github.com/drilonrecica/nise-and-go" go.mod 2>/dev/null; then
    go mod edit -replace github.com/drilonrecica/nise-and-go="$repo_root"
fi

# Download and tidy dependencies
go mod tidy > /dev/null 2>&1 || true

# Build the generated app binary
gen_app_bin="$gen_project_dir/cmd/testapp/testapp"
gen_app_size=0
startup_median=0

if go build -o "$gen_app_bin" ./cmd/testapp 2>&1; then
    if [[ -f "$gen_app_bin" ]]; then
        gen_app_size=$(stat -c%s "$gen_app_bin" 2>/dev/null || wc -c < "$gen_app_bin")
        echo "Generated app binary size: $gen_app_size bytes"

        # Measure cold startup to /healthz/ready readiness probe
        echo ""
        echo "=== Measuring generated app cold startup (to readiness probe) ==="
        startup_times=()
        for i in $(seq 1 "$samples"); do
            start_ns=$(date +%s%N)

            # Start the server in the background (uses default port 8080)
            PORT=8080 BIND_ADDR=127.0.0.1 "$gen_app_bin" -mode web > /tmp/app_startup_$i.log 2>&1 &
            server_pid=$!

            # Poll /healthz/ready until it returns 200, with a timeout
            ready=0
            for attempt in $(seq 1 100); do
                if curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/healthz/ready 2>/dev/null | grep -q "^200\$"; then
                    ready=1
                    break
                fi
                sleep 0.01
            done

            end_ns=$(date +%s%N)

            # Kill the server
            kill $server_pid 2>/dev/null || true
            wait $server_pid 2>/dev/null || true

            if [[ $ready -eq 1 ]]; then
                startup_ms=$(awk "BEGIN {printf \"%.3f\", ($end_ns - $start_ns) / 1000000}")
                startup_times+=("$startup_ms")
                printf "  Sample $i: %.3f ms\n" "$startup_ms"
            else
                printf "  Sample $i: FAILED (readiness probe did not respond)\n"
            fi
        done

        if [[ ${#startup_times[@]} -gt 0 ]]; then
            startup_median=$(printf '%s\n' "${startup_times[@]}" | sort -n | awk '{a[NR]=$1} END {if (NR % 2 == 1) print a[(NR+1)/2]; else print (a[NR/2] + a[NR/2+1])/2}')
            echo "Median: $startup_median ms"
        else
            startup_median=0
            echo "Median: FAILED (no successful samples)"
        fi

        # Measure idle RSS after startup
        echo ""
        echo "=== Measuring generated app idle RSS ==="
        rss_values=()
        for i in $(seq 1 "$samples"); do
            # Start the server in the background (uses default port 8080)
            PORT=8080 BIND_ADDR=127.0.0.1 "$gen_app_bin" -mode web > /tmp/app_rss_$i.log 2>&1 &
            server_pid=$!

            # Wait for readiness
            ready=0
            for attempt in $(seq 1 100); do
                if curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/healthz/ready 2>/dev/null | grep -q "^200\$"; then
                    ready=1
                    break
                fi
                sleep 0.01
            done

            if [[ $ready -eq 1 ]]; then
                # Let it settle for 100ms
                sleep 0.1

                # Read RSS from /proc/pid/status (VmRSS is in KiB)
                rss_kb=$(awk '/^VmRSS:/ {print $2}' /proc/$server_pid/status 2>/dev/null || echo "0")
                if [[ "$rss_kb" != "0" ]]; then
                    rss_mb=$(awk "BEGIN {printf \"%.1f\", $rss_kb / 1024}")
                    rss_values+=("$rss_mb")
                    printf "  Sample $i: %.1f MB\n" "$rss_mb"
                else
                    printf "  Sample $i: FAILED (could not read RSS)\n"
                fi
            else
                printf "  Sample $i: FAILED (readiness probe did not respond)\n"
            fi

            # Kill the server
            kill $server_pid 2>/dev/null || true
            wait $server_pid 2>/dev/null || true
        done

        if [[ ${#rss_values[@]} -gt 0 ]]; then
            rss_median=$(printf '%s\n' "${rss_values[@]}" | sort -n | awk '{a[NR]=$1} END {if (NR % 2 == 1) print a[(NR+1)/2]; else print (a[NR/2] + a[NR/2+1])/2}')
            echo "Median: $rss_median MB"
        else
            rss_median=0
            echo "Median: FAILED (no successful samples)"
        fi
    fi
else
    echo "WARNING: Generated app build failed; skipping startup and RSS measurements"
    startup_median=0
    rss_median=0
fi
echo ""

# ============================================================================
# PART 7: Output results
# ============================================================================

echo "=== Summary Table ==="
cat << EOF
Metric                                    Value           Unit
─────────────────────────────────────────────────────────────────
nise CLI size (unstripped)                $nise_size_unstripped       bytes
nise CLI size (stripped)                  $nise_size_stripped         bytes
nise version latency (warm median)        $version_latency_median     ms
nise new duration (median)                $new_median         ms
  (min/max)                               $new_min/$new_max         ms
Generated app binary size                 $gen_app_size       bytes
Generated app cold startup (median)       $startup_median     ms
Generated app idle RSS (median)           $rss_median         MB
EOF

# Ensure output directory exists
mkdir -p "$output_dir"

# JSON output for machine parsing
json_output="$output_dir/baseline-metrics.json"
cat > "$json_output" << EOF
{
  "metadata": {
    "commit": "$commit",
    "version": "$version",
    "go_version": "$(go version | awk '{print $3}')",
    "goos": "${GOOS:-$(go env GOOS)}",
    "goarch": "${GOARCH:-$(go env GOARCH)}",
    "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "samples": $samples
  },
  "cli": {
    "binary_size_bytes_unstripped": $nise_size_unstripped,
    "binary_size_bytes_stripped": $nise_size_stripped,
    "version_latency_ms_median": $version_latency_median,
    "version_latency_samples": $(printf '[%s]' "$(IFS=,; echo "${version_latencies[*]}")"),
    "new_duration_ms_median": $new_median,
    "new_duration_samples": $(printf '[%s]' "$(IFS=,; echo "${new_durations[*]}")")
  },
  "generated_app": {
    "binary_size_bytes": $gen_app_size,
    "startup_ms_median": $startup_median,
    "rss_mb_median": $rss_median
  }
}
EOF

echo ""
echo "Machine-readable output: $json_output"
