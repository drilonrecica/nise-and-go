# Repository rulesets

These are GitHub repository rulesets, exported as JSON. They are committed
because a branch-protection setting that exists only in a web form is a claim
nobody can review, and one nobody notices being turned off.

They are **not** applied automatically. GitHub has no mechanism to sync a
committed ruleset into a repository, so these files are the intended state and
a maintainer imports them by hand:

```
Settings → Rules → Rulesets → New ruleset → Import a ruleset
```

`test/release` reads them and fails if they stop requiring what
[`docs/repository-security.md`](../../docs/repository-security.md) says the
repository requires. That test is what makes the files worth committing: it
cannot prove the ruleset is *active* on GitHub — nothing in a repository can —
but it does prove the two documents have not drifted, which is the failure that
actually happens.

Verify the live state the way anybody else can:

```sh
gh api repos/drilonrecica/nise-and-go/rulesets --jq '.[].name'
gh api repos/drilonrecica/nise-and-go/rulesets/<id> > /tmp/live.json
```

| File | Protects |
|---|---|
| `master.json` | The default branch |
| `tags.json` | Release tags (`v*`) |
