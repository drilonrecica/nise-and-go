package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2Version is the algorithm version this package writes and accepts. It
// is Argon2's own version number (0x13), not a Nise parameter-set label.
const argon2Version = argon2.Version

// Errors reported by hashing and verification.
var (
	// ErrPasswordLength reports an empty password or one beyond
	// [MaxPasswordBytes].
	ErrPasswordLength = errors.New("password length is outside the permitted bounds")
	// ErrEncoding reports a stored hash that is not a well-formed Argon2id
	// PHC string.
	ErrEncoding = errors.New("stored password hash is not a well-formed argon2id encoding")
	// ErrUnsupportedParams reports a stored hash whose cost parameters are
	// not in the policy. Retiring a parameter set is what makes its records
	// unverifiable, deliberately.
	ErrUnsupportedParams = errors.New("stored password hash uses parameters this policy does not accept")
	// ErrAlgorithm reports a stored hash that is not argon2id, or is a
	// different Argon2 version.
	ErrAlgorithm = errors.New("stored password hash is not argon2id at the supported version")
)

// Verification is the outcome of checking one password against one stored
// hash.
type Verification struct {
	// Match reports whether the password produced the stored tag.
	Match bool
	// NeedsRehash reports that the hash was written under a superseded
	// parameter set. It is meaningful only when Match is true: that is the
	// one moment the server holds the plaintext and may write a new record.
	NeedsRehash bool
	// Version is the label of the parameter set the stored hash used.
	Version int
}

// Policy is the set of Argon2id parameter versions one deployment accepts,
// with exactly one that new hashes are written under.
//
// The zero value is unusable; construct one with [NewPolicy].
type Policy struct {
	current    Params
	accepted   []Params
	dummyHash  string
	dummyValue string
}

// NewPolicy returns a policy that hashes with current and also verifies
// records written under the superseded sets.
//
// A superseded set is a promise: as long as it is listed, records written
// under it still let their owners in. Removing one locks those accounts out of
// password login until they reset, which is sometimes exactly what an operator
// intends and must never happen by accident.
func NewPolicy(current Params, superseded ...Params) (*Policy, error) {
	if current.IsZero() {
		return nil, fmt.Errorf("%w: the current parameter set is not constructed", ErrParams)
	}
	accepted := make([]Params, 0, 1+len(superseded))
	accepted = append(accepted, current)
	for _, set := range superseded {
		if set.IsZero() {
			return nil, fmt.Errorf("%w: a superseded parameter set is not constructed", ErrParams)
		}
		accepted = append(accepted, set)
	}
	if err := distinct(accepted); err != nil {
		return nil, err
	}

	policy := &Policy{current: current, accepted: accepted}
	// The dummy is hashed once, at construction, under the current
	// parameters: VerifyDummy must cost what a real verification costs, and
	// paying for it per request would make the constant-time property depend
	// on request volume.
	value, err := randomString()
	if err != nil {
		return nil, err
	}
	hashed, err := policy.hashWith(current, value)
	if err != nil {
		return nil, err
	}
	policy.dummyValue = value
	policy.dummyHash = hashed
	return policy, nil
}

// Current returns the parameter set new hashes are written under.
func (p *Policy) Current() Params { return p.current }

// Accepted returns every parameter set this policy verifies, current first.
func (p *Policy) Accepted() []Params { return slices.Clone(p.accepted) }

// Versions returns the labels of every accepted parameter set, sorted.
func (p *Policy) Versions() []int { return versions(p.accepted) }

// Hash derives a new stored hash under the current parameter set.
func (p *Policy) Hash(secret string) (string, error) {
	return p.hashWith(p.current, secret)
}

func (p *Policy) hashWith(params Params, secret string) (string, error) {
	if err := checkSecret(secret); err != nil {
		return "", err
	}
	salt := make([]byte, SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating password salt: %w", err)
	}
	tag := argon2.IDKey([]byte(secret), salt, params.iterations, params.memoryKiB, params.parallelism, KeyBytes)
	return encode(params, salt, tag), nil
}

// Verify checks secret against a stored hash.
//
// A mismatch is reported through [Verification.Match], not through an error: a
// wrong password is an ordinary outcome, and making callers distinguish it
// from a malformed record by inspecting an error is how a login handler ends
// up treating a storage bug as a failed login.
func (p *Policy) Verify(encoded, secret string) (Verification, error) {
	if err := checkSecret(secret); err != nil {
		return Verification{}, err
	}
	params, salt, tag, err := p.decode(encoded)
	if err != nil {
		return Verification{}, err
	}

	tagLength := len(tag)
	if tagLength < MinTagBytes || tagLength > MaxTagBytes {
		return Verification{}, fmt.Errorf("%w: tag", ErrEncoding)
	}
	derived := argon2.IDKey([]byte(secret), salt, params.iterations, params.memoryKiB, params.parallelism, uint32(tagLength))
	return Verification{
		Match:       subtle.ConstantTimeCompare(derived, tag) == 1,
		NeedsRehash: !params.sameCost(p.current),
		Version:     params.version,
	}, nil
}

// VerifyDummy performs the same work Verify would, against a throwaway hash,
// and discards the result.
//
// Call it on the path where no account exists. Skipping the hash there is what
// turns response time into an account-existence oracle, and no amount of
// identical response bodies fixes that.
func (p *Policy) VerifyDummy(secret string) {
	// The result is deliberately discarded, including the error: this call
	// exists for the time it takes, and a caller that could branch on it
	// would reintroduce the distinction it removes.
	_, _ = p.Verify(p.dummyHash, clampSecret(secret))
}

// decode parses a stored hash and resolves its parameter set against the
// policy.
func (p *Policy) decode(encoded string) (Params, []byte, []byte, error) {
	fields := strings.Split(encoded, "$")
	if len(fields) != 6 || fields[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: %d fields", ErrEncoding, len(fields))
	}
	if fields[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: algorithm %q", ErrAlgorithm, fields[1])
	}

	rawVersion, found := strings.CutPrefix(fields[2], "v=")
	if !found {
		return Params{}, nil, nil, fmt.Errorf("%w: version field %q", ErrEncoding, fields[2])
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: version field %q", ErrEncoding, fields[2])
	}
	if version != argon2Version {
		return Params{}, nil, nil, fmt.Errorf("%w: argon2 version %d", ErrAlgorithm, version)
	}

	memoryKiB, iterations, parallelism, err := parseCost(fields[3])
	if err != nil {
		return Params{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(fields[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: salt", ErrEncoding)
	}
	tag, err := base64.RawStdEncoding.Strict().DecodeString(fields[5])
	if err != nil || len(tag) < MinTagBytes || len(tag) > MaxTagBytes {
		return Params{}, nil, nil, fmt.Errorf("%w: tag", ErrEncoding)
	}
	if len(salt) > MaxSaltBytes {
		return Params{}, nil, nil, fmt.Errorf("%w: salt", ErrEncoding)
	}

	stored := Params{memoryKiB: memoryKiB, iterations: iterations, parallelism: parallelism}
	for _, candidate := range p.accepted {
		if candidate.sameCost(stored) {
			return candidate, salt, tag, nil
		}
	}
	return Params{}, nil, nil, fmt.Errorf("%w: m=%d,t=%d,p=%d", ErrUnsupportedParams, memoryKiB, iterations, parallelism)
}

// parseCost reads the "m=...,t=...,p=..." field, in that exact order and with
// no extras: a record whose parameters this code half-understood is a record
// it must not verify against.
func parseCost(field string) (memoryKiB, iterations uint32, parallelism uint8, err error) {
	parts := strings.Split(field, ",")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("%w: cost field %q", ErrEncoding, field)
	}
	values := make([]uint32, 3)
	for i, prefix := range []string{"m=", "t=", "p="} {
		raw, found := strings.CutPrefix(parts[i], prefix)
		if !found {
			return 0, 0, 0, fmt.Errorf("%w: cost field %q", ErrEncoding, field)
		}
		// A 32-bit parse is what keeps every conversion below in range;
		// the cost triple is attacker-adjacent data read back from storage.
		parsed, parseErr := strconv.ParseUint(raw, 10, 32)
		if parseErr != nil {
			return 0, 0, 0, fmt.Errorf("%w: cost field %q", ErrEncoding, field)
		}
		values[i] = uint32(parsed)
	}
	rawParallelism := values[2]
	if rawParallelism < MinParallelism || rawParallelism > MaxParallelism {
		return 0, 0, 0, fmt.Errorf("%w: parallelism %d", ErrEncoding, rawParallelism)
	}
	return values[0], values[1], uint8(rawParallelism), nil
}

func encode(params Params, salt, tag []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		params.memoryKiB, params.iterations, params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(tag),
	)
}

func checkSecret(secret string) error {
	if secret == "" || len(secret) > MaxPasswordBytes {
		return fmt.Errorf("%w: %d bytes, want 1..%d", ErrPasswordLength, len(secret), MaxPasswordBytes)
	}
	return nil
}

// clampSecret makes any input usable by the dummy path, so that a password
// which would be rejected on length still costs an attacker the same wait.
func clampSecret(secret string) string {
	if secret == "" {
		return "\x00"
	}
	if len(secret) > MaxPasswordBytes {
		return secret[:MaxPasswordBytes]
	}
	return secret
}

func randomString() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generating dummy password value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
