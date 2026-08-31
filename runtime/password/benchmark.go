package password

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
)

// Bounds on a benchmark run. They exist so a mistyped target or a pathological
// machine produces an error instead of an hour of hashing.
const (
	// MinSamples is the fewest evaluations a measurement may average over.
	MinSamples = 1
	// MaxSamples is the most evaluations a measurement may average over.
	MaxSamples = 32
	// MinTarget is the shortest target duration [Recommend] accepts.
	MinTarget = 10 * time.Millisecond
	// MaxTarget is the longest target duration [Recommend] accepts. A login
	// that costs more than this is a denial-of-service surface of its own.
	MaxTarget = 2 * time.Second
)

// Errors reported by benchmarking.
var (
	// ErrSamples reports a sample count outside the permitted bounds.
	ErrSamples = errors.New("benchmark sample count is outside the permitted bounds")
	// ErrTarget reports a target duration outside the permitted bounds.
	ErrTarget = errors.New("benchmark target duration is outside the permitted bounds")
	// ErrUnreachableTarget reports that even the minimum parameter set is
	// slower than the target on this machine.
	ErrUnreachableTarget = errors.New("no accepted parameter set is fast enough for the target on this machine")
)

// Measurement is one timed Argon2id evaluation.
type Measurement struct {
	// Params is the set that was measured.
	Params Params
	// Duration is the median evaluation time.
	Duration time.Duration
	// Samples is how many evaluations the median was taken over.
	Samples int
}

// Recommendation is what a benchmark concluded for one machine.
type Recommendation struct {
	// Params is the strongest accepted set whose median stays within Target.
	Params Params
	// Measured is that set's median evaluation time.
	Measured time.Duration
	// Target is the budget the search was given.
	Target time.Duration
	// AtCeiling reports that the search stopped at [MaxIterations] rather
	// than at the target, so the machine has room the bounds do not allow.
	// Raise memory instead.
	AtCeiling bool
	// Measurements are every set the search evaluated, in order.
	Measurements []Measurement
}

// Benchmark times params on this machine.
//
// It reports the median rather than the mean: one sample delayed by an
// unrelated process should not raise the recommendation for every login
// afterwards.
func Benchmark(params Params, samples int) (Measurement, error) {
	if params.IsZero() {
		return Measurement{}, fmt.Errorf("%w: the parameter set is not constructed", ErrParams)
	}
	if samples < MinSamples || samples > MaxSamples {
		return Measurement{}, fmt.Errorf("%w: %d is outside %d..%d", ErrSamples, samples, MinSamples, MaxSamples)
	}

	secret := []byte("benchmark-password-benchmark-password")
	salt := make([]byte, SaltBytes)
	durations := make([]time.Duration, 0, samples)
	for range samples {
		start := time.Now()
		tag := argon2.IDKey(secret, salt, params.iterations, params.memoryKiB, params.parallelism, KeyBytes)
		elapsed := time.Since(start)
		// Keep the result observable so no compiler or runtime can decide
		// the work above was dead.
		if len(tag) != KeyBytes {
			return Measurement{}, errors.New("argon2id produced an unexpected tag length")
		}
		durations = append(durations, elapsed)
	}
	return Measurement{Params: params, Duration: median(durations), Samples: samples}, nil
}

// Recommend searches for the strongest parameter set this machine can evaluate
// within target, at the given memory cost and parallelism.
//
// Memory is held fixed and iterations are raised, because memory is the cost an
// attacker with parallel hardware finds hardest to buy: choose the largest
// memory a login can afford first, then let this find the passes that fit.
func Recommend(memoryKiB uint32, parallelism uint8, target time.Duration, samples int) (Recommendation, error) {
	if target < MinTarget || target > MaxTarget {
		return Recommendation{}, fmt.Errorf("%w: %s is outside %s..%s", ErrTarget, target, MinTarget, MaxTarget)
	}

	recommendation := Recommendation{Target: target}
	var best Params
	var bestDuration time.Duration
	for iterations := uint32(MinIterations); iterations <= MaxIterations; iterations++ {
		params, err := NewParams(1, memoryKiB, iterations, parallelism)
		if err != nil {
			return Recommendation{}, err
		}
		measurement, err := Benchmark(params, samples)
		if err != nil {
			return Recommendation{}, err
		}
		recommendation.Measurements = append(recommendation.Measurements, measurement)
		if measurement.Duration > target {
			break
		}
		best, bestDuration = params, measurement.Duration
		if iterations == MaxIterations {
			recommendation.AtCeiling = true
		}
	}
	if best.IsZero() {
		return Recommendation{}, fmt.Errorf("%w: m=%d,p=%d takes %s, target is %s",
			ErrUnreachableTarget, memoryKiB, parallelism,
			recommendation.Measurements[0].Duration, target)
	}
	recommendation.Params = best
	recommendation.Measured = bestDuration
	return recommendation, nil
}

func median(durations []time.Duration) time.Duration {
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2]
}
