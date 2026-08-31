# Reauthentication

A session answers one question: was a credential presented. It does not answer
whether the person who presented it is still there. Twelve hours of idle time is
the right shape for ordinary work and far too long to be the only thing between
an unlocked laptop and somebody granting themselves a role.

Reauthentication closes that gap for a small number of actions by asking for the
password again immediately beforehand. [ADR
0021](adr/0021-reauthentication-matrix.md) records the decision, the rejected
alternatives, and the principle behind the list.

## The matrix

`internal/platform/reauth` declares the closed set of actions that require a
recent proof, and how recent:

| Action | Window | Why |
|---|---:|---|
| `roles.grant` | 5 minutes | The escalation path: the role granted can carry every permission the granter holds. |
| `users.enable` | 15 minutes | Turning a disabled account back on restores a way in. |
| `invitations.create` | 15 minutes | An invitation is a way in that outlives the request that made it. |

The list is short on purpose. Every entry is friction a real person pays every
time, and friction spent where it buys nothing is what teaches people to type
their password into whatever asks for it.

## What is deliberately not in it

**Anything that withdraws access.** Revoking a role, disabling an account,
revoking an invitation, and ending another account's sessions are all absent. A
proof is required for actions that create or widen access, and never for actions
that take it away: the withdrawal is the response to a compromise, and a control
that made the response slower than the attack would be helping the wrong party.

Enabling and disabling an account are the same method with different arguments,
so the check is on the argument — `SetStatus` asks for a proof when the target
status is active and not when it is disabled.

**Changing a password.** That action already takes the current password, which
is a stronger proof than a recent one. Asking for the same secret twice in one
form teaches the wrong lesson. A successful password change instead *records* a
proof on the session that made it, so the next sensitive action does not prompt
again.

**Reading anything.** A matrix that covers everything is one somebody turns off.

## The declaration and the check are one commit

```go
// internal/platform/reauth/matrix.go
ActionRolesGrant Action = "roles.grant"

// internal/features/auth/roles.go
if err := authorization.Require(ctx, authorization.RolesManage); err != nil {
	return err
}
if err := reauth.Require(ctx, reauth.ActionRolesGrant); err != nil {
	return err
}
```

The permission check runs first. "May you" and "is it still you" are separate
questions, and an action that asked only the second would let anybody who could
type a password do anything.

An action the matrix does not declare is **refused**, not allowed. A missing line
is then a loud failure rather than a control that quietly stopped existing. The
way to stop requiring a proof is to remove the check, which is visible in a diff
of the use case. A generator test fails when a declared action is checked
nowhere, or a checked action is declared nowhere.

## Where the proof lives

`sessions.proven_at` is a column on the session row, not on the account.

Proving yourself in one browser therefore leaves a session on another machine
exactly as stale as it was. That is the property that makes the control worth
having: an attacker holding a stolen session must not be handed a fresh proof
every time the real person reauthenticates somewhere else.

- **Issuing** a session records a proof. The person just typed the password, and
  asking again immediately would teach them only that the prompt is noise.
- **Rotation** carries the proof across. Replacing a token is neither a new proof
  nor the loss of one.
- **Recording** a proof moves no other column. It is not activity, and it must
  not extend the absolute deadline — otherwise hourly reauthentication would be
  a session that never ends.

## Reauthenticating

```go
provenAt, err := credentials.Reauthenticate(ctx, submittedPassword)
```

It is not a second sign-in. It issues nothing, sets no cookie, and takes no
account identifier: the account and the session both come from the resolved
request, so a handler cannot prove one person's password against another
person's session.

It runs through the same [throttle](throttling.md) as signing in, keyed on the
same address, so the endpoint cannot become an unmetered oracle for guesses the
sign-in page counts — and guesses spent here are gone from the sign-in budget
too. A wrong password gets the same generic refusal it gets at sign-in, and a
disabled account cannot prove itself at all.

Every attempt is [audited](audit.md): `session.reauthenticated` on success,
`session.reauthentication_denied` with the reason on failure. A denial is a
separate action from a refused sign-in because it happens inside a live session
and says something different — somebody holding that session could not produce
the password.

## One instant per request

The reauthentication position — the account, the session, the stored proof, and
the instant to judge it against — is resolved once, by middleware, from the same
session the authorization middleware reads:

```go
API: []httpapi.Middleware{
	sessionResolver.Middleware,
	authorizationResolver.Middleware,
	reauthResolver.Middleware,
	apiGuard.Middleware,
},
```

Two checks in one request that read the clock separately could disagree, and a
request would then be fresh enough for its first action and not its second.

The middleware never rejects. It attaches the position even when nobody is
signed in, so "not signed in" and "this middleware is not wired" stay two
different answers — and both of them still deny.

## Bounds

`runtime/session` refuses a window under 30 seconds or over 24 hours. Below the
floor the action becomes impossible rather than careful: the window has to
outlast being asked for a password, typing it, and being returned to the action.
Above the ceiling it is not reauthentication but a second session lifetime under
another name.

A proof exactly as old as its window is refused: at the boundary the safe
direction is to ask again. A proof timestamped slightly ahead of the reader's
clock is accepted, because that timestamp came from the server's own store and a
reader trailing the writer is a clock problem, not an attack.

## Adding an action

1. Declare it in `internal/platform/reauth/matrix.go` with a window.
2. Check it in the use case, after the permission check.

Both in one commit. Decide the window by asking what the action does, not how
important it feels: does it create or widen access, and how long should somebody
be able to keep doing it after proving themselves once?

## Related

- [Sessions](sessions.md)
- [Authorization](authorization.md)
- [Authentication throttling](throttling.md)
- [Audit log](audit.md)
- [Security model](security.md)
- [ADR 0021: Require a recent proof for actions that widen access](adr/0021-reauthentication-matrix.md)
