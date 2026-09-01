# Repository security and maintainer recovery

Everything in [checks.md](checks.md) protects the code. This page protects the
*repository* — the branch a release is cut from, the tag it is cut at, and the
one account that can do either.

It is written for a project with exactly one maintainer and no organization
([ADR 0007](adr/0007-module-path-and-owner.md)), which changes what is possible
and, more importantly, what is honest to claim.

## Branch and tag protection

The rules are committed as GitHub ruleset exports under
[`.github/rulesets/`](../.github/rulesets/), because a branch-protection
setting that lives only in a web form is a claim nobody can review, and one
nobody notices being turned off.

### The default branch

| Rule | What it prevents |
|---|---|
| Deletion blocked | The branch every release is cut from disappearing |
| Force push blocked | History being rewritten under a tag that already points into it |
| Linear history required | A merge commit hiding what was actually reviewed |
| Signed commits required | An unsigned commit attributed to the maintainer |
| Status checks required, strict | A merge that never ran `make check`, or ran it against stale ancestors |

The eight required checks are exactly `ci.yml`'s jobs. `test/release` fails if
the two lists stop matching, which is the drift that actually happens: a job is
added to CI, nobody adds it to the ruleset, and it silently stops gating
anything.

**Strict** matters more than it looks. Without it, a branch that passed CI
before an incompatible change landed on the default branch can still merge —
the checks passed, on a tree that no longer exists.

### Release tags

| Rule | What it prevents |
|---|---|
| Deletion blocked | A published version vanishing |
| Update blocked | `v0.1.0` being moved to point at different code |
| Force push blocked | The same, by another route |
| Signed tags required | A tag attributed to the maintainer that they did not make |

Tag immutability is the one that matters most here, because a moved tag is the
classic supply-chain attack against a Go project: `go install …@v0.1.0`
resolves a tag, and a tag that moved after publication serves different code
under a version people have already audited.

Two independent things make that fail rather than succeed. This ruleset is the
first. The second is not in this repository at all — see below.

### Applying them

GitHub has no mechanism to sync a committed ruleset into a repository, so these
files are the intended state and a maintainer imports them by hand
(`Settings → Rules → Rulesets → Import a ruleset`). Nothing here can prove they
are *active*; that is checked the way anybody else can check it:

```sh
gh api repos/drilonrecica/nise-and-go/rulesets --jq '.[] | {name, enforcement}'
```

## What protects a release, honestly

Four controls, in increasing order of how much they actually withstand.

**1. Draft releases.** The release workflow creates a draft; nothing is
installable until a human presses publish. This defends against a tag pushed by
mistake. It defends against nothing deliberate.

**2. Build attestations.** Every artifact carries a signed statement, in a
public transparency log, that these exact bytes came from this repository's
release workflow at a named commit. This defends against artifact
substitution — somebody replacing a file on the release page. It does **not**
defend against a compromised maintainer, because "this repository's workflow"
is precisely what a compromised maintainer's workflow is.

**3. Release validation, run on every publish.** The workflow re-checks the
previous release as well as the new one
([rollback validation](checks.md#rollback-validation)), so a quietly altered
older release is noticed at the next publish rather than by whoever needed to
roll back.

**4. The Go checksum database.** This is the strongest control here, and it is
the only one outside this repository's control — which is exactly why.

Once `go install …@v0.1.0` has been resolved by anybody, the module proxy has
cached that version's bytes and `sum.golang.org` has recorded their hash in an
append-only, publicly auditable log. After that, the tag can be moved, the
branch can be rewritten, and the repository can be deleted: the proxy keeps
serving the original bytes, and a mismatch is a hard `go` command failure on
every machine, not a warning.

So the honest summary is: **the module proxy is what makes a published Nise
release tamper-evident, and everything in this repository is what makes a
tamper *attempt* unlikely and visible.** Anybody who wants the strongest
guarantee should install with `go install` at an exact version and let the
checksum database be their witness.

## If the maintainer's account is compromised

Stated plainly, because the alternative is implying a defence that does not
exist: **an attacker holding the maintainer's GitHub account can do everything
this project can do.** They can push to the default branch, edit the release
workflow, tag, publish, and move the Homebrew tap. Every control above assumes
that account is not the attacker.

That is not unique to this project — it is the ordinary state of a
single-maintainer repository — but it is worth writing down rather than leaving
readers to infer it from a page about branch protection.

### What limits the damage

- **Hardware-backed second factor.** The account uses a security key or
  passkey, not TOTP or SMS. This is the single highest-value control on this
  page, because it is the one that makes the scenario in this section unlikely
  rather than merely detectable.
- **The tap token is scoped to the tap.** `HOMEBREW_TAP_TOKEN` is a
  fine-grained PAT with contents read/write on `drilonrecica/homebrew-tap` and
  nothing else. A leaked tap token can change one repository whose every commit
  is public.
- **Already-published module versions cannot be changed**, as above. An
  attacker can publish `v0.2.0`; they cannot alter `v0.1.0` for anybody whose
  toolchain has already seen it.
- **Attestations are timestamped in a public log.** A substituted artifact
  carries a *new* attestation with a later timestamp, which is a visible
  discrepancy rather than a silent replacement.

### The procedure

1. **Regain or lock the account.** GitHub account recovery, and revoke every
   session, PAT, SSH key, and OAuth app authorization. Rotate
   `HOMEBREW_TAP_TOKEN` regardless of whether it was used.
2. **Establish what changed.** Compare the default branch against a local
   clone; list every release and every tag with `gh release list` and
   `git ls-remote --tags`, and compare tag SHAs against a clone taken before
   the incident. This is why a maintainer keeps a full local clone, and why
   tags are protected — a clone is the only copy an attacker with repository
   access cannot edit.
3. **Verify each published artifact's attestation** and its timestamp. An
   artifact whose attestation post-dates the release's publication is one that
   was replaced.
4. **Say so publicly, and quickly.** A `SECURITY.md` note and a GitHub Security
   Advisory naming the affected versions and the window. Users can verify their
   own copies (`gh attestation verify`, `go mod verify`); they cannot know to.
5. **Do not delete the compromised release.** Deleting it removes the evidence
   and breaks the checksum database's view of it. Publish a new patch version
   and mark the old one clearly.
6. **Rotate the `BACKUP_ENCRYPTION_KEY`** of any deployment whose secrets could
   have been read from repository or Actions configuration
   ([key custody and rotation](backups.md#key-custody-and-rotation)).

### If the maintainer is unavailable

There is no second maintainer, no bus factor, and no succession plan, because
inventing one for a personal project would be a promise rather than a fact.

What exists instead is the MIT licence and a repository with no private parts:
anybody may fork it and continue, and everything needed to do so — the
generator, the templates, the tests, the release workflow, and the reasoning in
`docs/` — is in the repository. A fork cannot inherit the module path, the
Homebrew tap, or the attestation identity, and should not: a new maintainer's
releases should be verifiably theirs.

[`GOVERNANCE.md`](../GOVERNANCE.md) already states there is no support, review,
release, or security-response SLA. This is the operational shape of that
sentence.

### Recovery material

A recovery plan stored only inside the account being recovered is not one. So
the account's recovery codes, and the tap repository's, live where the
[backup key](backups.md#key-custody-and-rotation) lives: off the machines
involved, retrievable without them, and written into a runbook whose location
somebody other than the maintainer knows.

The same rule as for the backup key applies, for the same reason: an
arrangement nobody has exercised is a belief about an arrangement. Retrieving
the recovery codes belongs in the same drill as retrieving the backup key.

## Related

- [Checks](checks.md) — what CI, the release, and the tap enforce
- [Threat model](threat-model.md)
- [Security](security.md)
- [Backups](backups.md#key-custody-and-rotation)
- [Governance](../GOVERNANCE.md)
