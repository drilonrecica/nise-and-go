# Authorization

Permissions are the primitive. A permission names one capability —
`invoices.create` — and is what code checks. A role is a name for a set of them,
and exists so that granting somebody "billing clerk" is one decision instead of
eleven.

**Nothing lets a check ask about a role.** A use case that asked "is this person
an administrator" would have encoded an organization chart in a place that has
to be rewritten every time the chart changes, and would give the wrong answer
for every deployment whose chart is different. Use cases check permissions;
roles exist only to grant them.

## The catalog is closed

Every permission the application checks is declared once, in
`internal/platform/authorization`, and a role may only bundle permissions the
catalog declares. Without that rule a typo in a role definition grants a
permission nothing ever checks: the grant reads correctly in review, the check
reads correctly in review, and the two never meet. `NewCatalog` refuses it, so
it is a startup failure instead.

The same reasoning is why **there are no wildcards**. A permission pattern like
`invoices.*` is how an accidental grant becomes invisible — nobody reviewing it
can see what it will mean after the next feature lands.

## Definitions are code, assignments are data

| | Where | Why |
|---|---|---|
| Permission names | Go | Decided by the use case that checks them; reviewed with that code. |
| Role bundles | Go | A role's meaning is a decision worth reviewing, and it must be identical on every replica. |
| Who holds a role | `user_roles` | Changes as people join and leave; an administrator changes it without a deploy. |

Adding a permission is two edits in one commit: declare it in the catalog, and
check it in the use case that needs it.

## The starter catalog

| Permission | Meaning |
|---|---|
| `users.read` | Read account records other than one's own. |
| `users.manage` | Enable, disable, and enrol accounts. |
| `roles.read` | See who holds which role. |
| `roles.manage` | Grant and revoke roles — the permission that leads to every other one. |
| `sessions.revoke` | End another account's sessions. |
| `audit.read` | Read the audit log. |

| Role | Holds |
|---|---|
| `administrator` | Everything above. |
| `auditor` | The three reads, and nothing that changes anything. |

`audit.read` is separate from `users.manage` on purpose: the people who can
change things and the people who can see what was changed do not have to be the
same, and in some organizations must not be. The `auditor` role is what makes
"look, do not touch" expressible, which is what makes it safe to grant widely.

Delete what this application does not have and add what it does. Nothing outside
that package depends on these particular names.

## Default deny is the empty case

An account with no roles holds no permissions. That is not a rule applied on top
of a lookup — it is what the lookup returns. The zero `authz.Set` holds nothing,
and an invalid or zero-valued permission is never reported as held, so a caller
that forgot to resolve someone's permissions authorizes nothing rather than
everything.

## Stale grants

A `user_roles` row naming a role the catalog no longer declares contributes
nothing, and is reported separately so the caller can log it.

Refusing would lock an account out because of a role definition somebody
removed. Ignoring it silently would hide the removal. Reporting it does neither.
The row stays revocable, or it would be permanent.

## Related

- [Sessions](sessions.md)
- [Audit log](audit.md)
- [Security model](security.md)
