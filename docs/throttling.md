# Authentication Throttling

Throttling exists for one attack: somebody working through a list of addresses
and passwords. Argon2 makes each guess expensive for the server as well as the
attacker, so an unthrottled login endpoint is simultaneously a way in and a way
to exhaust the machine.

**The check runs before the hash, not after it.** A limiter consulted after the
password work would stop the attacker getting in and would not stop them tying
up every worker in the process.

## Two buckets

| Bucket | Default | Catches |
|---|---:|---|
| Per address | 10 attempts / 15 min | A run of guesses at one account, from anywhere. |
| Per client | 60 attempts / 15 min | A run across many accounts, from one place. |

Either can refuse, and **both are always counted** — a caller cannot avoid the
client counter by exhausting the address one first.

The client limit is normally the looser of the two: several people behind one
office address are one client, and a limit tight enough for a single account
would lock out a building. Configuring it tighter warns at startup.

## What it does and does not tell an attacker

A throttled attempt returns a *different* error from a failed one — it is not a
statement about the credentials at all, and a caller should answer it with a
retry hint rather than a rejection. It still says nothing about whether the
address exists: the counter is keyed by a digest of whatever was submitted, so
an unknown address throttles exactly like a known one.

While the window is open, **even the correct password is refused**. A throttle
that stepped aside for a correct guess would not be one.

`RetryAfter` is the refusing window's remainder, deliberately, rather than a
precise per-attempt value. A limiter that told an attacker exactly when to try
again would be helping.

## Counters live in PostgreSQL

A counter that disappears when a process restarts is a counter an attacker can
clear, and adding a cache to make login slower for attackers would make the
whole application unavailable whenever that cache is.

**The subject is a digest, never the value.** An address in the throttle table
would turn it into a list of every address anybody has tried to sign in as,
which is a directory an attacker would like to read.

The increment is an upsert returning the new count, so concurrent attempts are
all counted — a lost update there would be a free guess for an attacker running
requests in parallel, which is how they would run them.

**A refused attempt still costs a slot.** A limiter that only counted the
attempts it allowed would let an attacker sit at the boundary forever.

Counting runs in its own transaction, separate from the login. A failed login
rolls its own work back, and the count of that failure has to survive it.

## Fixed windows, stated plainly

The window is fixed, not sliding. An attacker who times a burst across a
boundary can make twice the limit in quick succession and then nothing for the
rest of the next window. A sliding window would close that at the cost of
storing every attempt; for an authentication limiter the fixed window's failure
mode is a brief doubling, which is not the difference between safe and unsafe.

## Resets

A successful login clears the **address** counter, so somebody who mistyped
twice and then got in is not locked out by their next mistake.

It does not clear the client counter. One success from a machine working through
a list must not buy that machine another sixty guesses.

## No client address

A command-line login has no request context and therefore no client address.
Only the address bucket applies. Refusing outright would make an operator's own
tooling unusable; counting it under a shared placeholder would let one such
caller lock out every other.

## Audit

Every attempt is recorded: `session.created` on success, `session.denied` on
failure with the reason — `no_account`, `wrong_password`, `disabled`,
`unverifiable`, or `throttled`. A denial is a **separate action** rather than a
failed outcome of the first, so a query for "when did this person sign in" does
not have to filter out the attempts that were not them.

The submitted address is never in the detail. A failed attempt for an address
with no account would otherwise put an arbitrary submitted string in the log.

An audit write that fails is logged loudly and does not change the login's
outcome: a login that succeeded must not become a failure because the record did
not land, and one that failed must not be retried because of it.

## Related

- [Sessions](sessions.md)
- [Password hashing](passwords.md)
- [Audit log](audit.md)
