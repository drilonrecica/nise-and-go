# Enrollment

Registration is closed. An account exists because somebody with `users.manage`
invited it, or because the first-administrator bootstrap created it on an empty
directory. There is no self-service sign-up path, and adding one is a deliberate
decision an application makes rather than a default it inherits.

## Invitations

An administrator invites an address and gets a token back **once**. Only its
SHA-256 digest is stored, for the same reason a session stores only a digest: a
leak of the table must not hand anybody a working invitation.

The invitation token is a **distinct type** from a session token, with its own
`inv1_` prefix. That is the point of the separation: if both were the same type,
a lookup that consulted the wrong table would compile, and a credential that can
be presented in two places is a credential that will be.

| Property | Value |
|---|---|
| Lifetime | 7 days by default, 1 hour to 30 days |
| Roles | validated against the catalog when the invitation is created |
| Outstanding per address | exactly one |

Roles are checked at creation, not at acceptance. An invitation promising a role
that does not exist would silently grant nothing to somebody who has already
chosen a password.

Inviting an address that already has an account is refused at creation for the
same reason: the link would fail on acceptance, after the person had gone to the
trouble.

One outstanding invitation per address, enforced by a partial unique index. Two
live links to the same person is not a feature; it is two links, one of which
quietly stops working.

## Acceptance

Acceptance is the one unauthenticated write in this feature, and every part of
it happens in a single transaction: the invitation row is locked `FOR UPDATE`,
the account is created, its roles are granted, and the invitation is closed. Any
failure leaves none of it, and four people following the same link concurrently
create exactly one account.

**Every reason an invitation cannot be used is the same error** — unknown,
malformed, expired, already accepted, withdrawn. Distinguishing them would let
somebody with a guessed link learn which guesses were close.

The password rules run *before* anything is written, so a refused password
leaves the link usable rather than burning it.

## The first administrator

```sh
myapp admin bootstrap --email founder@example.test
```

It works only on an empty directory, and prints the invitation token once.

**Rerunning it is allowed while the directory is still empty.** A bootstrap
token that could be issued only once would brick a deployment whose operator
lost it, with nobody able to revoke or reissue. What rerunning must not do is
leave the earlier link alive — that link would still work after the first
administrator accepted and the directory stopped being empty, which is a second
unauthenticated route in. So every outstanding invitation is withdrawn first,
and only the newest link works.

Once any account exists the command refuses, permanently.

## Retention

`Sweep` removes invitations nobody used, in bounded batches. Accepted ones stay:
they are the record of how an account came to exist.

## Related

- [Sessions](sessions.md)
- [Authorization](authorization.md)
- [Password hashing](passwords.md)
