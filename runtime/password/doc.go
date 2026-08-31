// Package password owns Argon2id password hashing, its versioned parameter
// sets, and the benchmark that chooses them.
//
// # Why parameters are versioned
//
// A password hash is stored for as long as the account exists, which is longer
// than any parameter choice stays adequate. Hardware gets faster; the cost that
// was painful for an attacker in 2026 is not painful in 2031. So a deployment
// has to be able to raise its parameters without invalidating every stored
// hash, and has to be able to tell, at login, that a particular record was
// written under an older set.
//
// A [Policy] holds exactly one current parameter set plus the superseded sets
// it still accepts. Verifying a hash written under a superseded set succeeds
// and reports [Verification.NeedsRehash], which is the caller's signal to
// rehash the password it just proved it holds. A hash written under a set that
// is not in the policy at all fails: dropping a set from the list is the
// explicit, auditable way an operator retires it.
//
// # Why the encoding is the standard one
//
// Hashes are stored in the PHC string format Argon2's reference implementation
// defines:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<base64 salt>$<base64 hash>
//
// The parameters travel with the hash, so verification never depends on
// guessing which set produced a record, and a deployment that leaves Nise can
// read its own password column with any Argon2 library. A private format would
// buy nothing and lock data in.
//
// # Why there is a dummy verification
//
// [Policy.VerifyDummy] runs the same Argon2 work against a throwaway hash. A
// login for an address with no account must cost what a login for a real one
// costs, or the response time answers "does this person have an account here"
// to anyone who asks.
//
// # Benchmarking
//
// [Recommend] measures this machine and reports the strongest parameters that
// stay inside a target duration. Parameters are a property of the hardware that
// runs them, so the answer belongs to the deployment, not to this package: what
// ships here is a floor that meets current OWASP guidance, not a claim about
// anyone's server.
package password
