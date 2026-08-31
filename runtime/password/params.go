package password

import (
	"errors"
	"fmt"
	"slices"
)

// Bounds every parameter set is held to. The minimums are OWASP's current
// Argon2id floor; the maximums exist so a typo in configuration is a startup
// failure rather than a server that allocates 4 TiB per login attempt.
const (
	// MinMemoryKiB is the smallest memory cost accepted, in kibibytes.
	MinMemoryKiB = 19 * 1024
	// MaxMemoryKiB is the largest memory cost accepted, in kibibytes.
	MaxMemoryKiB = 4 * 1024 * 1024
	// MinIterations is the smallest time cost accepted.
	MinIterations = 1
	// MaxIterations is the largest time cost accepted.
	MaxIterations = 64
	// MinParallelism is the smallest degree of parallelism accepted.
	MinParallelism = 1
	// MaxParallelism is the largest degree of parallelism accepted.
	MaxParallelism = 64
	// MaxVersion is the largest parameter-set version label accepted.
	MaxVersion = 1000
)

// Sizes of the values this package generates. They are constants rather than
// parameters because neither is a tuning knob: 16 bytes of salt is the
// reference recommendation, and a 32-byte tag is the width the comparison
// needs.
const (
	// SaltBytes is the length of the random salt in every new hash.
	SaltBytes = 16
	// KeyBytes is the length of the derived tag in every new hash.
	KeyBytes = 32
	// MinTagBytes and MaxTagBytes bound the tag length a stored hash may
	// declare. Bounding it is what lets the verifier hand the length to
	// Argon2 without a widening conversion that could wrap.
	MinTagBytes = 16
	MaxTagBytes = 1024
	// MaxSaltBytes bounds the salt length a stored hash may declare, for the
	// same reason.
	MaxSaltBytes = 1024
)

// MaxPasswordBytes bounds an accepted password. Argon2's cost does not grow
// with input length, so this is not about work; it is about refusing an
// unbounded value at the boundary rather than carrying it further in.
const MaxPasswordBytes = 1024

// Errors reported by parameter and policy construction.
var (
	// ErrParams reports a parameter outside the bounds above.
	ErrParams = errors.New("argon2id parameters are outside the permitted bounds")
	// ErrDuplicateVersion reports two parameter sets sharing a version label
	// or the same cost triple.
	ErrDuplicateVersion = errors.New("argon2id parameter sets are not distinct")
)

// Params is one immutable, versioned Argon2id parameter set.
//
// version is a label an operator chooses, not something the format carries: a
// stored hash is matched to a set by its cost triple, which travels with it.
// The label is what documentation, configuration, and the benchmark output
// call the set.
type Params struct {
	version     int
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

// NewParams validates one Argon2id parameter set.
func NewParams(version int, memoryKiB, iterations uint32, parallelism uint8) (Params, error) {
	switch {
	case version < 1 || version > MaxVersion:
		return Params{}, fmt.Errorf("%w: version %d is outside 1..%d", ErrParams, version, MaxVersion)
	case memoryKiB < MinMemoryKiB || memoryKiB > MaxMemoryKiB:
		return Params{}, fmt.Errorf("%w: memory %d KiB is outside %d..%d", ErrParams, memoryKiB, MinMemoryKiB, MaxMemoryKiB)
	case iterations < MinIterations || iterations > MaxIterations:
		return Params{}, fmt.Errorf("%w: iterations %d is outside %d..%d", ErrParams, iterations, MinIterations, MaxIterations)
	case parallelism < MinParallelism || parallelism > MaxParallelism:
		return Params{}, fmt.Errorf("%w: parallelism %d is outside %d..%d", ErrParams, parallelism, MinParallelism, MaxParallelism)
	}
	return Params{version: version, memoryKiB: memoryKiB, iterations: iterations, parallelism: parallelism}, nil
}

// Default returns the parameter set this package ships as version 1.
//
// It is OWASP's second recommended Argon2id configuration (46 MiB, one pass,
// one lane) raised to three passes, which is a deliberate floor rather than a
// recommendation for any particular machine. Run the benchmark on the hardware
// that will serve logins and raise it.
func Default() Params {
	return Params{version: 1, memoryKiB: 47104, iterations: 3, parallelism: 1}
}

// Version returns the operator-chosen label for this parameter set.
func (p Params) Version() int { return p.version }

// MemoryKiB returns the memory cost in kibibytes.
func (p Params) MemoryKiB() uint32 { return p.memoryKiB }

// Iterations returns the time cost.
func (p Params) Iterations() uint32 { return p.iterations }

// Parallelism returns the degree of parallelism.
func (p Params) Parallelism() uint8 { return p.parallelism }

// IsZero reports whether p is the unconstructed zero value.
func (p Params) IsZero() bool { return p.version == 0 }

// String renders the set the way the benchmark and the documentation name it.
func (p Params) String() string {
	return fmt.Sprintf("v%d m=%d,t=%d,p=%d", p.version, p.memoryKiB, p.iterations, p.parallelism)
}

// sameCost reports whether two sets would produce interchangeable hashes. The
// version label is deliberately not part of it: the label is bookkeeping, the
// cost triple is what a stored hash actually records.
func (p Params) sameCost(other Params) bool {
	return p.memoryKiB == other.memoryKiB &&
		p.iterations == other.iterations &&
		p.parallelism == other.parallelism
}

// distinct reports whether every set in the list has a unique version label
// and a unique cost triple.
func distinct(sets []Params) error {
	for i, a := range sets {
		for _, b := range sets[i+1:] {
			if a.version == b.version {
				return fmt.Errorf("%w: version %d appears twice", ErrDuplicateVersion, a.version)
			}
			if a.sameCost(b) {
				return fmt.Errorf("%w: versions %d and %d have the same cost", ErrDuplicateVersion, a.version, b.version)
			}
		}
	}
	return nil
}

// versions returns the labels of sets, sorted, for reporting.
func versions(sets []Params) []int {
	out := make([]int, 0, len(sets))
	for _, set := range sets {
		out = append(out, set.version)
	}
	slices.Sort(out)
	return out
}
