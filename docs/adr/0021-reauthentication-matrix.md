# 0021: Require a recent proof for actions that widen access, and for nothing else

- **Status:** Accepted
- **Date:** 2026-08-31
- **Closes:** the `THREAT_MODEL` open question on the reauthentication matrix for sensitive actions (M5-013)

## Context

A session answers one question: was a credential presented. It does not answer
whether the person who presented it is still there. The default session policy
is twelve hours idle and thirty days absolute, which is the right shape for
ordinary work and much too long to be the only thing standing between an
unlocked laptop and an administrator granting themselves a role.

Reauthentication closes that gap by asking for the password again immediately
before certain actions. The decision is not whether to have it — the blueprint
already says sensitive actions may require it — but which actions, how recent
the proof must be, where the proof is stored, and what happens when the
declaration and the code disagree.

Every entry in such a matrix is friction a real person pays repeatedly. Friction
spent where it buys nothing is not neutral: it is what teaches people to type
their password into whatever asks for it, which is the habit phishing depends
on.

## Decision

### The matrix is a closed declaration in application code

`internal/platform/reauth` declares the actions requiring a proof and the window
each will accept. It is code, not configuration, for the same reason the
permission catalog is: an action's meaning is decided by the use case that
checks it, so the two must be reviewed together, and every replica must agree.

Adding an action is two edits in one commit — declare it, and check it — and a
generator test fails when the two drift apart in either direction.

### An undeclared action denies

`reauth.Require` refuses an action the matrix does not declare, rather than
allowing it. A missing line is then a loud failure — nobody can perform the
action — instead of a control that silently stopped existing. The way to stop
requiring a proof is to remove the check, which is visible in a diff of the use
case, not to remove the declaration, which is visible only if somebody is
looking for an absence.

### Three actions, and the principle behind the list

| Action | Window | Why |
| --- | --- | --- |
| `roles.grant` | 5 minutes | The escalation path: the role granted can carry every permission the granter holds. |
| `users.enable` | 15 minutes | Turning a disabled account back on restores a way in. |
| `invitations.create` | 15 minutes | An invitation is a way in that outlives the request that made it. |

The principle: **a proof is required for actions that create or widen access,
and never for actions that withdraw it.** Revoking a role, disabling an account,
revoking an invitation, and ending another account's sessions are all
deliberately absent. Withdrawal is the response to a compromise, and a control
that made the response slower than the attack would be helping the wrong party.

Enabling and disabling an account are the same method with different arguments,
so the check is on the argument: `SetStatus` asks for a proof when the target
status is active and not when it is disabled.

Changing a password is absent for a different reason: that action already takes
the current password, which is a stronger proof than a recent one. Asking for
the same secret twice in one form teaches the wrong lesson. Instead, a
successful password change **records** a proof on the session that made it.

Reading is absent entirely. A matrix that covers everything is one somebody
turns off.

### The proof lives on the session

`sessions.proven_at` is a column on the session row, not on the account. Proving
yourself in one browser therefore leaves a session on another machine exactly as
stale as it was, which is the property that makes the control worth having:
otherwise an attacker holding a stolen session would be handed a fresh proof
every time the real person reauthenticated somewhere else.

Issuing a session records a proof, because the person just typed the password.
Rotation carries the proof across, because replacing a token is neither a new
proof nor the loss of one. Recording a proof moves no other column: it is not
activity, and it must not extend the absolute deadline, or hourly
reauthentication would be a session that never ends.

### Reauthenticating is not a second sign-in

`Credentials.Reauthenticate` issues nothing, sets no cookie, and takes no
account identifier: the account and the session both come from the resolved
request, so a handler cannot prove one person's password against another
person's session. It runs through the same throttle as signing in, keyed on the
same address, so the endpoint cannot be used as an unmetered oracle for guesses
that the sign-in page counts. It refuses with the same generic error, and it
refuses a disabled account.

### One instant per request

The reauthentication position — the account, the session, the stored proof, and
the instant to judge it against — is resolved once by middleware, from the same
session the authorization middleware reads. Two checks in one request that read
the clock separately could disagree, and a request would then be fresh enough
for its first action and not its second.

### Windows are bounded

`runtime/session` refuses a window under 30 seconds or over 24 hours. Below the
floor the action becomes impossible rather than careful; above the ceiling it is
not reauthentication but a second session lifetime under another name.

## Alternatives considered

- **Key the matrix on permissions.** Rejected: the axes do not line up. Changing
  your own password is sensitive and needs no permission; reading the audit log
  needs one and no proof. A permission-keyed matrix would also make every check
  of a permission demand a proof, including the reads that share it.
- **A single session-wide "recently authenticated" flag.** Rejected: it makes
  every window the same, which means the window is set by the most tolerant
  action on the list.
- **Step-up with a second factor instead of the password.** Deferred, not
  rejected. The optional TOTP module (M5-011) can satisfy the same window; this
  ADR fixes where the proof is recorded and which actions need one, which is the
  part a second factor plugs into rather than replaces.
- **Make the windows configurable.** Rejected for the same reason the permission
  catalog is not configurable: the value is meaningless without the check it
  belongs to, and an operator lengthening it to a week has removed the control
  without touching the code that claims to have it.
- **Fail open on an undeclared action.** Rejected, as above.

## Consequences

- An attacker who obtains a live administrative session can read, and can
  withdraw access, but cannot grant a role, enable an account, or create an
  invitation without the password.
- An administrator doing a batch of grants reauthenticates every five minutes.
  That is the cost, and it is why the list is three entries long.
- Adding an action to the matrix is a code change with a test that enforces the
  pairing; changing the list itself is an ADR-sized decision, and a generator
  test says so.
- A generated application that adds its own sensitive actions has a declared
  place to put them and a documented principle for deciding.
