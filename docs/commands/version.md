# `nise version`

Prints which nise this is.

```sh
nise version
```

```
nise v0.1.0 (commit 9d405a4e6774ceef17984fbbfd7ef94f827aa8c9, built 2026-09-01T06:14:52Z)
```

```sh
nise --json version
```

```json
{"version":"v0.1.0","commit":"9d405a4e…","date":"2026-09-01T06:14:52Z"}
```

The version carries the tag's `v` whichever way nise was installed. That is not
cosmetic: `nise new` writes this exact string into the project recipe, and
`nise doctor` compares it later — so two installation channels reporting one
release differently would make the same release generate two different project
trees. See
[cli-and-distribution.md](../cli-and-distribution.md#every-channel-serves-the-same-release).

A `commit` and `date` are present when nise was installed from a release
archive or through Homebrew, and absent for `go install`, which compiles from a
module zip that carries no version control metadata. A build from a checkout
reports `dev`.

## `nise version check`

Asks whether a newer release exists, and prints the command to install it for
the way you installed nise.

```sh
nise version check
```

```
nise v0.1.0 is installed; v0.2.0 is the latest release.

Installed through: homebrew
  brew update && brew upgrade nise

Release notes: https://github.com/drilonrecica/nise-and-go/releases/tag/v0.2.0

nise changed nothing. It never installs or replaces a binary.
```

**This is the only nise command that uses the network.** Every other command
works offline, and nothing runs this one for you: there is no background check,
no check on startup, and no cached answer — an update check that remembered
when it last looked would need a file to remember it in, and nise writes none
(see [no-telemetry.md](../no-telemetry.md)).

It does not install anything, and the output says so. Nise never modifies a
binary, least of all one a package manager owns: `brew upgrade` is Homebrew's
job, and a tool that reaches into another tool's install tree is a tool you
cannot uninstall cleanly.

### Why it is `nise version check` and not `nise update`

Two reasons, and the first is the one that decided it.

`nise upgrade` already exists and upgrades a *generated project* — its
dependencies and its source. Naming this one `nise update` would put two
near-synonyms in the same CLI meaning entirely different things, one of which
edits your application. A longer name is a smaller problem.

And it does not update. Naming a command for something it deliberately refuses
to do is how a reader ends up believing it did.

### What it sends

One `GET` to `https://api.github.com/repos/drilonrecica/nise-and-go/releases/latest`,
carrying:

- `Accept: application/vnd.github+json`
- `X-GitHub-Api-Version: 2022-11-28`
- `User-Agent: nise` — a constant, with **no version in it**

The User-Agent is the deliberate part. Putting the running version there would
make every update check report which nise the caller has, which is the shape of
the thing this project says it does not do, arriving through the one command
permitted to open a socket. A constant makes every user's request identical, so
the header tells GitHub nothing the TCP connection did not.

No authentication, no cookies, no request body. Redirects are refused rather
than followed: this endpoint does not redirect, and a redirect is how a captive
portal or an intercepting proxy steers a request somewhere else — so nise talks
to that one host or fails. The response is read under a size limit and the tag
it names is checked against the shape of a release tag before being echoed back
to you as something to install.

`test/nonetwork` proves both halves of this: that every other command reaches
nothing, and that this one reaches exactly one host, once. An allowlist entry
for a package that no longer dials would be a permission nobody reviewed still
standing, so the check that it *does* dial is as load-bearing as the checks that
nothing else does.

### Installation channels

The instruction depends on how nise got onto the machine, because a wrong one
is worse than none — `brew upgrade` on a binary Homebrew does not own does
nothing and reads like a bug in nise.

| Detected as | Because | You are told to run |
|---|---|---|
| `homebrew` | The binary's own path is inside a Homebrew tree | `brew update && brew upgrade nise` |
| `archive` | The binary carries a release build's stamped commit, and is not in a Homebrew tree | `gh release download <tag> …`, then replace it |
| `go-install` | No release stamp, but the toolchain recorded a module version | `go install …/cmd/nise@<tag>` |
| `source` | The toolchain recorded `(devel)` | `git fetch --tags && git checkout <tag>` |
| `unknown` | None of the above | all of them, and it says it could not tell |

The signal doing the real work is one this project learned by testing its own
channels rather than by reading them: the release build stamps a commit through
`-ldflags`, and `go install` cannot, because a module zip carries no version
control metadata for the toolchain to record. Homebrew is checked first, since
a Homebrew binary *is* the release archive's binary — only its location on disk
tells them apart.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The check completed. Read `outdated` in `--json` output for the answer. |
| 1 | The release index could not be reached, or has published nothing. |

Being out of date is not a failure, so it is not an error exit. A script that
wants the answer reads it:

```sh
nise --json version check | jq -r 'if .outdated then .howTo[] else "current" end'
```

### When it cannot reach the network

```
nise could not reach the release index
Check your network connection and try again. This is the only nise command that needs one; everything else works offline.
```

The second line is deliberate. Somebody who ran one command that needs a
network should not be left wondering which of their commands need one.
