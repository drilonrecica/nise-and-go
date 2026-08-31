# 0023: Second-factor design — HMAC-SHA1, a stored secret, and a single-use challenge

- **Status:** Accepted
- **Date:** 2026-08-31

## Context

The optional TOTP module adds a second step to signing in. Four choices in it are the kind that are easy to get wrong quietly, and each has an answer that looks worse on paper than the alternative.

## Decision

### HMAC-SHA1, fixed, with the reason stated

RFC 6238 permits SHA-1, SHA-256, and SHA-512. Every authenticator application in practical use implements SHA-1; only some implement the others, and several silently compute SHA-1 regardless of what the provisioning URI asks for — which produces codes that never match and an enrollment nobody can debug.

The module fixes SHA-1, and the documentation says plainly that this is a compatibility decision rather than a cryptographic preference. It is not a weakness here: TOTP uses HMAC, whose security rests on the key rather than on the hash's collision resistance, and the SHA-1 collision work says nothing about HMAC-SHA1. The attack on a six-digit code from a 160-bit key is guessing one of a million values in thirty seconds, which is a rate-limiting problem.

Period and digits are likewise fixed at thirty seconds and six. Both are carried in the provisioning URI, both are ignored by some authenticators, and a deployment that changed either would find out from users whose codes stopped working.

### The shared secret is stored as issued

The secret sits in `totp_secrets.secret` in the clear. Encrypting it with a key that lives beside the application defends only against a leak of the database alone — and that is a real scenario, usually a backup — but the same leak already carries the password hashes and the whole session table, and a key in the same environment as the process that reads it is a modest obstacle in exchange for a key-generation, key-distribution, and key-rotation story that every deployment then has to get right.

This is a residual risk, recorded as one, not an oversight: an attacker who reads this table can compute codes for every enrolled account until each one re-enrolls. The seam for envelope encryption is `internal/features/mfa`, which is application-owned code, and an owner who needs it has the file.

The recovery codes are the opposite case and are stored as SHA-256 digests, because a digest is enough to check one.

### Recovery codes are hashed like tokens, not like passwords

A recovery code is fifty bits from a cryptographic source in a fixed alphabet. There is no dictionary to search and nothing for a slow hash to defend, so it is stored as SHA-256 — the same reasoning that makes a session token's digest a plain hash. Argon2id belongs to secrets a human chose.

The alphabet is Crockford's base32 (no I, L, O, or U), and parsing folds case, discards hyphens and spaces, and maps `I`/`L` to `1` and `O` to `0`. Somebody typing a code off a printed sheet is transcribing, not authenticating; refusing an unambiguous transcription error sends them to support instead of into their account.

A set is replaced whole, never topped up. A code from a list somebody thought had leaked must stop working the moment they ask for a new list.

### The second step is carried by a single-use challenge

A correct password produces a challenge, not a session. The challenge is a distinct credential type — its own prefix, its own table, its own digest — for the same reason session and invitation tokens are distinct from each other: a credential that can be presented in two places will be, and a lookup against the wrong table would otherwise compile.

It lives five minutes, is deleted on success, and carries its own attempt budget of five. The budget is on the **challenge**, not on the account: a stranger who could spend somebody's account-wide attempt budget by failing a second step against them would have a lockout tool. When the budget runs out the challenge is deleted rather than left to refuse, so there is nothing left to test guesses against.

A wrong code is counted in its own transaction. The transaction that checked the code is about to roll back, and a budget that only recorded the guesses that happened to commit would not be a budget — the same lesson the [authentication throttle](../throttling.md) learned about counting failed attempts.

### Replay is prevented by storing the accepted step

A TOTP code is arithmetically valid for its whole period. Without a stored high-water mark the same six digits work twice, which makes it a short-lived password rather than a one-time code. The module stores the step each accepted code came from and refuses anything at or below it, in the same statement that records it, so two concurrent submissions of one code cannot both win.

The code that confirms an enrollment spends its step too, so the digits somebody just typed cannot also complete their next sign-in.

## Alternatives considered

- **SHA-256 codes.** Rejected: broken authenticators, no meaningful gain.
- **Configurable period and digits.** Rejected: they are compatibility hazards, and the failure appears in support tickets rather than tests.
- **Encrypting the secret with an application-held key.** Rejected for now, with the residual risk written down rather than quietly accepted. A key managed outside the application would change the answer; that is an owner's decision and an owner's seam.
- **Argon2id for recovery codes.** Rejected: it defends nothing here and makes verifying one cost as much as a password.
- **Reusing the session token type for the challenge.** Rejected: one credential type presentable in two places.
- **Counting attempts per account.** Rejected: it is a lockout tool for a stranger.
- **Letting a correct password issue the session and requiring the code afterwards.** Rejected outright: that is a second factor in name only, since the session already works.

## Consequences

- An attacker with a correct password and no authenticator gets a challenge that expires in five minutes and dies after five guesses.
- An attacker who reads the database can compute codes; that is stated in the threat model rather than implied to be impossible.
- A person who loses their phone uses a recovery code, and the audit log records which method finished the sign-in, so "a recovery code was used" is a signal somebody can alert on.
- Removing a second factor requires a recent proof of identity ([ADR 0021](0021-reauthentication-matrix.md)); adding one requires nothing, because adding one narrows what a stolen session can do.
