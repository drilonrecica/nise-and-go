# Second factor: TOTP and recovery codes

The optional `totp` module adds a second step to signing in: a six-digit code
from an authenticator application, or one of ten printed single-use recovery
codes.

```sh
nise new myapp --module totp
```

It is a [compile-time module](adr/0022-compile-time-modules.md). A project
generated without it contains no file that mentions it — not a stub, not a
build-tagged placeholder, not an unused import — and a generator test says so.
[ADR 0023](adr/0023-second-factor-design.md) records the design decisions and
what each one costs.

## What it adds

| Path | What it is |
|---|---|
| `db/migrations/00010_totp.sql` | `totp_secrets`, `recovery_codes`, `mfa_challenges` |
| `internal/features/mfa/` | the use case, its queries, and its sqlc output |
| `internal/app/secondfactor.go` | the wiring — application-owned, yours to edit |

The primitives live in the framework's `modules/totp` package: codes, secrets,
recovery-code generation. Nothing there stores anything or decides anything.

## Signing in

```go
login, err := credentials.Authenticate(ctx, email, password)
if errors.Is(err, auth.ErrSecondFactorRequired) {
	// login.Challenge.Token carries the second step. No session was issued.
}
```

A correct password produces a **challenge**, not a session. The error is
deliberate: a caller checking only `err` would otherwise treat a half-finished
sign-in as a finished one, which is the whole failure a second factor exists to
prevent.

```go
login, err := credentials.CompleteSecondFactor(ctx, challengeToken, submitted)
```

The session is issued here and nowhere else. `login.SecondFactorMethod` reports
`totp` or `recovery_code`.

The challenge lives five minutes, is single-use, and carries its own budget of
five attempts. The budget is on the challenge rather than on the account, so a
stranger cannot lock somebody out by failing the second step against them. When
it runs out the challenge is deleted rather than left refusing, so there is
nothing left to test guesses against — and a wrong code is counted in its own
transaction, because the transaction that checked it is about to roll back and a
budget that only counted committed guesses would not be a budget.

The address counter of the [sign-in throttle](throttling.md) is cleared by a
*finished* sign-in, not by a correct password.

## Enrolling

```go
enrollment, err := factors.Enrol(ctx, userID, emailAddress)
// enrollment.ProvisioningURI is what the QR code encodes.
codes, err := factors.Confirm(ctx, userID, submittedCode)
// codes is the recovery-code list, returned once.
```

An enrolled secret is **not** a second factor until it is confirmed. Nothing
depends on it in between, which is what stops somebody being locked out by an
enrollment they started, mis-scanned, and abandoned. Enrolling again before
confirming replaces the secret; enrolling over a confirmed factor is refused.

The stored secret is never handed back. Somebody who has lost their
authenticator enrols a new secret; they do not ask for the old one.

The code that confirms the enrollment spends its own thirty-second step, so the
digits somebody just typed cannot also complete their next sign-in.

## One-time means one time

A TOTP code is arithmetically valid for its whole period. The module stores the
step each accepted code came from and refuses anything at or below it, in the
same statement that records it — so two concurrent submissions of one code
cannot both win, and a code read over a shoulder or pulled from a proxy log is
already spent.

Without that, a "one-time code" is a password that lasts thirty seconds.

## Recovery codes

Ten codes, fifty bits each, shown once:

```
K7M2P-9XQ4R
```

They are stored as SHA-256 digests — a digest is enough to check one, and there
is no dictionary to search, which is why they are not run through Argon2id like
a password. Each is single-use, enforced by the `used_at IS NULL` guard in the
statement that spends it rather than by a read followed by a write.

The alphabet is Crockford's base32 (no `I`, `L`, `O`, or `U`), and parsing folds
case, discards hyphens and spaces, and maps `I`/`L` to `1` and `O` to `0`.
Somebody typing a code off a printed sheet is transcribing, not authenticating.

`RegenerateRecoveryCodes` replaces the whole set. A code from a list somebody
thought had leaked stops working the moment they ask for a new list.
`UnusedRecoveryCodes` is what tells somebody they are running out before they
run out.

## Removing a factor

`Disable` requires a recent proof of identity — it is in the
[reauthentication matrix](reauthentication.md) as `second_factor.disable`, on
that matrix's own principle: removing a second factor widens what a stolen
session can do. **Adding** one narrows it, and asks for nothing.

Disabling removes the recovery codes with the factor. A printed list that
outlived the factor it belonged to would be a way in nobody remembers granting.

## Audit

| Action | When |
|---|---|
| `session.challenged` | a correct password opened a second step |
| `session.created` | a finished sign-in, with `second_factor` in the detail |
| `second_factor.enrolled` | a confirmed factor |
| `second_factor.disabled` | a removed one |
| `second_factor.recovery_codes_issued` | a replaced set |
| `second_factor.recovery_code_used` | a code spent |

`session.challenged` is its own action rather than a successful sign-in or a
denial, because recording it as either would make one of those two counts wrong.
`second_factor.recovery_code_used` is worth alerting on: it means somebody has
lost their authenticator — or that somebody else has their printed list.

A refused second step names no account. An invented challenge token must not let
a stranger write audit rows against somebody they guessed.

## HMAC-SHA1, and why

RFC 6238 allows SHA-1, SHA-256, and SHA-512. Every authenticator application
implements SHA-1; only some implement the others, and several compute SHA-1
regardless of what the provisioning URI says — which produces codes that never
match and an enrollment nobody can debug.

The module fixes SHA-1. That is a compatibility decision, not a cryptographic
preference, and it is not a weakness here: TOTP uses HMAC, whose security rests
on the key rather than on the hash's collision resistance, and the SHA-1
collision work says nothing about HMAC-SHA1.

Period and digits are fixed at thirty seconds and six for the same reason.

## What the secret at rest costs you

The shared secret is stored as issued. Encrypting it with a key that lives
beside the application defends only against a leak of the database alone — a
real scenario, usually a backup — but that same leak already carries the
password hashes and the session table, and a key in the same environment as the
process reading it buys less than the key-rotation story it obliges every
deployment to get right.

So: **an attacker who reads `totp_secrets` can compute codes for every enrolled
account until each one re-enrols.** That is a residual risk, written down rather
than implied away. `internal/features/mfa` is application-owned, so an owner who
needs envelope encryption has the file.

Recovery codes are the opposite case and are never stored in a recoverable form.

## An application without the module

The login use case still names what it does not have:

```go
// internal/app/secondfactor.go
func SecondFactor(*database.Transactor, auth.Auditor, string) (auth.SecondFactor, error) {
	return auth.NoSecondFactor{}, nil
}
```

`NewCredentials` refuses a nil second factor. An optional dependency defaulting
to nil would make "this application has no second factor" indistinguishable from
"somebody forgot to wire it".

## Related

- [Sessions](sessions.md)
- [Reauthentication](reauthentication.md)
- [Authentication throttling](throttling.md)
- [Audit log](audit.md)
- [ADR 0022: A compile-time module is a framework package plus conditional templates](adr/0022-compile-time-modules.md)
- [ADR 0023: Second-factor design](adr/0023-second-factor-design.md)
