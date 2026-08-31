// Package session owns opaque session tokens and the driver-independent rules
// that decide whether a stored session is still usable.
//
// # Opaque, not self-describing
//
// A session token is 32 random bytes and nothing else. It says nothing about
// who holds it, when it was issued, or what it permits; every one of those
// answers comes from the row the server looks up. That is the whole point of
// [ADR 0006's] choice against JWTs: a self-describing credential cannot be
// revoked without the very server-side lookup it was adopted to avoid, and a
// signature-verification bug in it is an authentication bypass.
//
// # Only the digest is stored
//
// The server stores SHA-256 of the token, never the token. A database leak
// therefore does not hand an attacker a working credential, and a log line or
// a backup that captures the session table captures nothing that can be
// replayed.
//
// The digest is a plain hash, not a keyed one, because the token is 256 bits of
// uniform randomness: there is no dictionary to search and nothing for a pepper
// to defend. Password hashing is the opposite case, and uses Argon2id.
//
// # Two clocks, both stored
//
// A session has an idle bound and an absolute bound, and both are enforced from
// the row rather than recomputed from configuration. The absolute deadline is
// written when the session is created, so shortening the configured lifetime
// does not silently extend sessions that already exist — and lengthening it
// does not either.
//
// [Record.Evaluate] reports the strongest reason a session is unusable, in the
// order revoked, expired, idle. A caller that only asked "is it active" would
// have nothing to log and nothing to tell the user.
//
// # Touching
//
// Sliding a session's idle window on every request turns every authenticated
// GET into a database write. [Lifetime.NeedsTouch] answers whether enough time
// has passed to be worth one, which bounds that cost without meaningfully
// changing when a session expires.
package session
