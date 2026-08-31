package password_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/password"
)

func TestBenchmarkValidatesItsInputs(t *testing.T) {
	t.Parallel()

	if _, err := password.Benchmark(password.Params{}, 1); !errors.Is(err, password.ErrParams) {
		t.Errorf("Benchmark accepted an unconstructed parameter set: %v", err)
	}
	for _, samples := range []int{0, -1, password.MaxSamples + 1} {
		if _, err := password.Benchmark(testParams(t, 1, 1), samples); !errors.Is(err, password.ErrSamples) {
			t.Errorf("Benchmark(samples=%d) error = %v, want ErrSamples", samples, err)
		}
	}
}

func TestBenchmarkMeasuresRealWork(t *testing.T) {
	t.Parallel()

	measurement, err := password.Benchmark(testParams(t, 1, 1), 3)
	if err != nil {
		t.Fatalf("Benchmark: %v", err)
	}
	if measurement.Samples != 3 {
		t.Errorf("Samples = %d, want 3", measurement.Samples)
	}
	if measurement.Duration <= 0 {
		t.Fatalf("Duration = %s; the measured work was optimized away", measurement.Duration)
	}
	if measurement.Params.Version() != 1 {
		t.Errorf("Params = %v, want the set that was measured", measurement.Params)
	}

	// More passes must cost more. If they do not, the loop is not doing the
	// work the recommendation is derived from.
	heavier, err := password.Benchmark(testParams(t, 1, 4), 3)
	if err != nil {
		t.Fatalf("Benchmark: %v", err)
	}
	if heavier.Duration <= measurement.Duration {
		t.Errorf("four passes took %s against one pass's %s", heavier.Duration, measurement.Duration)
	}
}

func TestRecommendValidatesItsTarget(t *testing.T) {
	t.Parallel()

	for _, target := range []time.Duration{0, -time.Second, password.MinTarget - time.Millisecond, password.MaxTarget + time.Second} {
		if _, err := password.Recommend(password.MinMemoryKiB, 1, target, 1); !errors.Is(err, password.ErrTarget) {
			t.Errorf("Recommend(target=%s) error = %v, want ErrTarget", target, err)
		}
	}
	if _, err := password.Recommend(password.MinMemoryKiB-1, 1, 100*time.Millisecond, 1); !errors.Is(err, password.ErrParams) {
		t.Errorf("Recommend accepted memory below the floor: %v", err)
	}
	if _, err := password.Recommend(password.MinMemoryKiB, 1, 100*time.Millisecond, 0); !errors.Is(err, password.ErrSamples) {
		t.Errorf("Recommend accepted zero samples: %v", err)
	}
}

func TestRecommendStaysWithinItsTarget(t *testing.T) {
	t.Parallel()

	// A modest target keeps the search a few passes deep. Walking all the way
	// to the iteration ceiling would spend half a minute proving the same
	// property, in a suite that runs on every commit.
	const target = 20 * password.MinTarget
	recommendation, err := password.Recommend(password.MinMemoryKiB, 1, target, 1)
	if errors.Is(err, password.ErrUnreachableTarget) {
		t.Skipf("skipping: this machine cannot hash at the accepted floor within %s", target)
	}
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if recommendation.Params.IsZero() {
		t.Fatal("Recommend returned no parameter set")
	}
	if recommendation.Measured > recommendation.Target {
		t.Errorf("recommended %v at %s, beyond the %s target", recommendation.Params, recommendation.Measured, recommendation.Target)
	}
	if recommendation.Params.MemoryKiB() != password.MinMemoryKiB || recommendation.Params.Parallelism() != 1 {
		t.Errorf("Recommend changed the fixed memory or parallelism: %v", recommendation.Params)
	}
	if len(recommendation.Measurements) == 0 {
		t.Error("Recommend reported no measurements")
	}
	for i, measurement := range recommendation.Measurements {
		if measurement.Params.Iterations() != uint32(i+1) {
			t.Errorf("measurement %d used t=%d; the search must raise iterations one at a time", i, measurement.Params.Iterations())
		}
	}
	// The search either stopped because the next set was too slow, or
	// because it hit the iteration ceiling. Those are the only two ways out.
	last := recommendation.Measurements[len(recommendation.Measurements)-1]
	if !recommendation.AtCeiling && last.Duration <= recommendation.Target {
		t.Errorf("the search stopped at %s, inside the %s target, without reporting the ceiling", last.Duration, recommendation.Target)
	}
	if recommendation.AtCeiling && recommendation.Params.Iterations() != password.MaxIterations {
		t.Errorf("AtCeiling with t=%d, want %d", recommendation.Params.Iterations(), password.MaxIterations)
	}
}

func TestRecommendReportsAnUnreachableTarget(t *testing.T) {
	t.Parallel()

	// The floor parameter set at the shortest permitted target: on any
	// machine slow enough that one pass at 19 MiB exceeds 10 ms, the honest
	// answer is that no accepted set fits, not a set below the floor.
	recommendation, err := password.Recommend(password.MinMemoryKiB, 1, password.MinTarget, 1)
	switch {
	case err == nil:
		if recommendation.Measured > recommendation.Target {
			t.Errorf("recommended %s for a %s target", recommendation.Measured, recommendation.Target)
		}
	case errors.Is(err, password.ErrUnreachableTarget):
		// Correct on a slow machine: refused rather than weakened.
	default:
		t.Fatalf("Recommend error = %v, want nil or ErrUnreachableTarget", err)
	}
}
