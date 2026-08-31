package password_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/password"
)

// testParams keeps every unit test at Argon2id's accepted floor. Real
// deployments run the benchmark; a test suite that hashed at production cost
// would spend minutes proving nothing extra.
func testParams(t *testing.T, version int, iterations uint32) password.Params {
	t.Helper()

	params, err := password.NewParams(version, password.MinMemoryKiB, iterations, 1)
	if err != nil {
		t.Fatalf("NewParams: %v", err)
	}
	return params
}

func testPolicy(t *testing.T, superseded ...password.Params) *password.Policy {
	t.Helper()

	policy, err := password.NewPolicy(testParams(t, 2, 2), superseded...)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func TestNewParamsBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     int
		memoryKiB   uint32
		iterations  uint32
		parallelism uint8
	}{
		{name: "version zero", version: 0, memoryKiB: password.MinMemoryKiB, iterations: 2, parallelism: 1},
		{name: "version above the ceiling", version: password.MaxVersion + 1, memoryKiB: password.MinMemoryKiB, iterations: 2, parallelism: 1},
		{name: "memory below the OWASP floor", version: 1, memoryKiB: password.MinMemoryKiB - 1, iterations: 2, parallelism: 1},
		{name: "memory above the ceiling", version: 1, memoryKiB: password.MaxMemoryKiB + 1, iterations: 2, parallelism: 1},
		{name: "no iterations", version: 1, memoryKiB: password.MinMemoryKiB, iterations: 0, parallelism: 1},
		{name: "iterations above the ceiling", version: 1, memoryKiB: password.MinMemoryKiB, iterations: password.MaxIterations + 1, parallelism: 1},
		{name: "no parallelism", version: 1, memoryKiB: password.MinMemoryKiB, iterations: 2, parallelism: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := password.NewParams(tc.version, tc.memoryKiB, tc.iterations, tc.parallelism); !errors.Is(err, password.ErrParams) {
				t.Fatalf("NewParams error = %v, want ErrParams", err)
			}
		})
	}

	params := testParams(t, 3, 4)
	if params.Version() != 3 || params.Iterations() != 4 || params.MemoryKiB() != password.MinMemoryKiB || params.Parallelism() != 1 {
		t.Fatalf("params = %#v", params)
	}
	if params.IsZero() {
		t.Error("a constructed parameter set reports IsZero")
	}
	if !(password.Params{}).IsZero() {
		t.Error("the zero parameter set does not report IsZero")
	}
	if got, want := params.String(), fmt.Sprintf("v3 m=%d,t=4,p=1", password.MinMemoryKiB); got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestDefaultParamsMeetTheFloor(t *testing.T) {
	t.Parallel()

	shipped := password.Default()
	if shipped.MemoryKiB() < password.MinMemoryKiB {
		t.Errorf("shipped memory %d is below the accepted floor %d", shipped.MemoryKiB(), password.MinMemoryKiB)
	}
	if shipped.Iterations() < 2 {
		t.Errorf("shipped iterations = %d; a single pass is below current guidance", shipped.Iterations())
	}
	if _, err := password.NewParams(shipped.Version(), shipped.MemoryKiB(), shipped.Iterations(), shipped.Parallelism()); err != nil {
		t.Errorf("the shipped default does not satisfy NewParams: %v", err)
	}
}

func TestNewPolicyRejectsAmbiguousHistories(t *testing.T) {
	t.Parallel()

	if _, err := password.NewPolicy(password.Params{}); !errors.Is(err, password.ErrParams) {
		t.Errorf("NewPolicy accepted an unconstructed current set: %v", err)
	}
	if _, err := password.NewPolicy(testParams(t, 1, 2), password.Params{}); !errors.Is(err, password.ErrParams) {
		t.Errorf("NewPolicy accepted an unconstructed superseded set: %v", err)
	}
	if _, err := password.NewPolicy(testParams(t, 1, 2), testParams(t, 1, 3)); !errors.Is(err, password.ErrDuplicateVersion) {
		t.Errorf("NewPolicy accepted a repeated version label: %v", err)
	}
	// Two sets with the same cost would make a stored hash ambiguous: it
	// would match both, and which one it "is" would depend on list order.
	if _, err := password.NewPolicy(testParams(t, 1, 2), testParams(t, 2, 2)); !errors.Is(err, password.ErrDuplicateVersion) {
		t.Errorf("NewPolicy accepted two sets with the same cost: %v", err)
	}

	policy := testPolicy(t, testParams(t, 1, 1))
	if got := policy.Current().Version(); got != 2 {
		t.Errorf("Current version = %d, want 2", got)
	}
	if got := policy.Versions(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("Versions = %v, want [1 2]", got)
	}
	if got := policy.Accepted(); len(got) != 2 || got[0].Version() != 2 {
		t.Errorf("Accepted = %v, want the current set first", got)
	}
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	const secret = "correct horse battery staple"

	encoded, err := policy.Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if strings.Contains(encoded, secret) {
		t.Fatalf("the encoded hash contains the password: %q", encoded)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("encoded = %q, want the standard PHC prefix", encoded)
	}

	verification, err := policy.Verify(encoded, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.Match || verification.NeedsRehash || verification.Version != 2 {
		t.Fatalf("verification = %#v, want a current-version match", verification)
	}

	wrong, err := policy.Verify(encoded, secret+"!")
	if err != nil {
		t.Fatalf("Verify with the wrong password returned an error: %v", err)
	}
	if wrong.Match {
		t.Fatal("the wrong password matched")
	}

	// A wrong password is an ordinary outcome, not an error: a caller must
	// not have to inspect an error to tell a failed login from a broken
	// record.
	if _, err := policy.Verify(encoded, "x"); err != nil {
		t.Errorf("a short wrong password produced an error: %v", err)
	}
}

func TestHashIsSaltedPerRecord(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	const secret = "the same password twice"

	first, err := policy.Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := policy.Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of one password are identical; the salt is not random")
	}
	for _, encoded := range []string{first, second} {
		verification, err := policy.Verify(encoded, secret)
		if err != nil || !verification.Match {
			t.Fatalf("Verify(%q) = %#v, %v", encoded, verification, err)
		}
	}
}

func TestVerifyReportsAnOutdatedParameterSet(t *testing.T) {
	t.Parallel()

	const secret = "a password that outlived its parameters"
	old := testParams(t, 1, 1)
	oldPolicy, err := password.NewPolicy(old)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	stored, err := oldPolicy.Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	// The new policy still lists the old set, so the record verifies and is
	// flagged for rehash.
	current := testPolicy(t, old)
	verification, err := current.Verify(stored, secret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.Match || !verification.NeedsRehash || verification.Version != 1 {
		t.Fatalf("verification = %#v, want a superseded-version match needing rehash", verification)
	}

	rehashed, err := current.Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	after, err := current.Verify(rehashed, secret)
	if err != nil {
		t.Fatalf("Verify after rehash: %v", err)
	}
	if !after.Match || after.NeedsRehash || after.Version != 2 {
		t.Fatalf("verification after rehash = %#v", after)
	}

	// Retiring the old set is what locks its records out. This is the whole
	// point of the superseded list being explicit.
	retired := testPolicy(t)
	if _, err := retired.Verify(stored, secret); !errors.Is(err, password.ErrUnsupportedParams) {
		t.Fatalf("a retired parameter set still verified: %v", err)
	}
}

func TestVerifyRejectsMalformedRecords(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	valid, err := policy.Hash("a password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	fields := strings.Split(valid, "$")

	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{name: "empty", encoded: "", want: password.ErrEncoding},
		{name: "not a PHC string", encoded: "hunter2", want: password.ErrEncoding},
		{name: "too few fields", encoded: strings.Join(fields[:5], "$"), want: password.ErrEncoding},
		{name: "too many fields", encoded: valid + "$extra", want: password.ErrEncoding},
		{name: "a different algorithm", encoded: strings.Replace(valid, "argon2id", "argon2i", 1), want: password.ErrAlgorithm},
		{name: "bcrypt", encoded: "$2a$10$abcdefghijklmnopqrstuv", want: password.ErrEncoding},
		{name: "a different argon2 version", encoded: strings.Replace(valid, "v=19", "v=16", 1), want: password.ErrAlgorithm},
		{name: "a non-numeric argon2 version", encoded: strings.Replace(valid, "v=19", "v=xx", 1), want: password.ErrEncoding},
		{name: "a missing version field", encoded: strings.Replace(valid, "v=19", "19", 1), want: password.ErrEncoding},
		{name: "a reordered cost field", encoded: strings.Replace(valid, "m=", "z=", 1), want: password.ErrEncoding},
		{name: "a non-numeric cost", encoded: strings.Replace(valid, ",t=2,", ",t=two,", 1), want: password.ErrEncoding},
		{name: "a salt that is not base64", encoded: strings.Join([]string{fields[0], fields[1], fields[2], fields[3], "not base64!", fields[5]}, "$"), want: password.ErrEncoding},
		{name: "an empty salt", encoded: strings.Join([]string{fields[0], fields[1], fields[2], fields[3], "", fields[5]}, "$"), want: password.ErrEncoding},
		{name: "a tag that is not base64", encoded: strings.Join([]string{fields[0], fields[1], fields[2], fields[3], fields[4], "not base64!"}, "$"), want: password.ErrEncoding},
		{name: "a truncated tag", encoded: strings.Join([]string{fields[0], fields[1], fields[2], fields[3], fields[4], base64.RawStdEncoding.EncodeToString([]byte("short"))}, "$"), want: password.ErrEncoding},
		{name: "padded base64", encoded: strings.Join([]string{fields[0], fields[1], fields[2], fields[3], base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")), fields[5]}, "$"), want: password.ErrEncoding},
		{name: "unaccepted cost", encoded: strings.Replace(valid, ",t=2,", ",t=7,", 1), want: password.ErrUnsupportedParams},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.Verify(tc.encoded, "a password"); !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPasswordLengthBounds(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	for _, secret := range []string{"", strings.Repeat("x", password.MaxPasswordBytes+1)} {
		if _, err := policy.Hash(secret); !errors.Is(err, password.ErrPasswordLength) {
			t.Errorf("Hash(%d bytes) error = %v, want ErrPasswordLength", len(secret), err)
		}
		if _, err := policy.Verify("$argon2id$v=19$m=1,t=1,p=1$AA$AA", secret); !errors.Is(err, password.ErrPasswordLength) {
			t.Errorf("Verify(%d bytes) error = %v, want ErrPasswordLength", len(secret), err)
		}
	}

	longest := strings.Repeat("x", password.MaxPasswordBytes)
	encoded, err := policy.Hash(longest)
	if err != nil {
		t.Fatalf("Hash at the maximum length: %v", err)
	}
	verification, err := policy.Verify(encoded, longest)
	if err != nil || !verification.Match {
		t.Fatalf("Verify at the maximum length = %#v, %v", verification, err)
	}
}

func TestVerifyDummyDoesTheSameWork(t *testing.T) {
	t.Parallel()

	policy := testPolicy(t)
	encoded, err := policy.Hash("a real account's password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	// The claim is that the unknown-account path costs what the known-account
	// path costs. Timing on a shared machine is noisy, so this asserts the
	// same order of magnitude rather than a tight bound: a VerifyDummy that
	// skipped the hash would be microseconds against milliseconds.
	realStart := time.Now()
	if _, err := policy.Verify(encoded, "the wrong password"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	real := time.Since(realStart)

	dummyStart := time.Now()
	policy.VerifyDummy("the wrong password")
	dummy := time.Since(dummyStart)

	if dummy*8 < real {
		t.Errorf("VerifyDummy took %s against a real verification's %s; the unknown-account path is measurably cheaper", dummy, real)
	}

	// It must also survive input Hash and Verify would reject outright,
	// because that input arrives from the same untrusted form.
	policy.VerifyDummy("")
	policy.VerifyDummy(strings.Repeat("x", password.MaxPasswordBytes+1))
}

func TestPolicyDummiesDifferBetweenInstances(t *testing.T) {
	t.Parallel()

	// Two processes must not share a dummy record: an identical dummy hash
	// across every deployment would be a fixed, publicly derivable value on
	// the one code path an unauthenticated attacker can always reach.
	first := testPolicy(t)
	second := testPolicy(t)
	firstHash, err := first.Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	secondHash, err := second.Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if firstHash == secondHash {
		t.Fatal("two policies produced identical hashes for one password")
	}
}
