# Linting

Four tools, each covering something the others cannot see. None of them is on
because it exists.

| Tool | What it checks | Where |
|---|---|---|
| `golangci-lint` | Go, with a curated linter set | `.golangci.yml` |
| `sqlc vet` | Feature-local SQL, against rules each `sqlc.yaml` declares | `make sqlc-vet` |
| Biome | TypeScript, and the `<script>` blocks inside `.svelte` | `frontend/biome.jsonc` |
| `svelte-check` | Svelte's own type checking, **including the markup** | `pnpm --dir frontend run check` |

Plus two purpose-built scripts under `frontend/scripts/`: one that refuses
pre-runes Svelte syntax, and one that enforces a colour-contrast floor in
`app.css`.

## The rule that keeps every list short

**Do not enable a linter that would force suppression comments across the
tree.** Either the code changes or the linter does not belong here.

A `//nolint` on every third function is not a stricter codebase; it is a
linter whose output nobody reads any more. When a rule is right in general and
wrong for a whole category of file, it is switched off **for that category, at
the config level, with the reason written down** — not suppressed one site at
a time.

Both configurations do this, and both say why in the file:

- `.golangci.yml` turns off gosec's `G304`/`G306` and `G101`/`G115` in
  `_test.go` files. A test's path is under `t.TempDir()`, its connection
  string is a literal for a database it created and will drop, and its integer
  conversions are over values it chose. All four rules stay at full strength
  in production code.
- `biome.jsonc` turns off `noUnusedVariables` and `noUnusedImports` for
  `.svelte` files. Biome parses the `<script>` block and not the markup, so it
  cannot see that `let { children } = $props()` is used by `{@render
  children()}`. Left on, those two rules report **187 findings in a freshly
  generated project**, none of them real. `svelte-check` does see the markup
  and does report a genuinely unused declaration, so the defect class is
  covered — by the tool that can actually tell.

Where a suppression really is per-site, it carries a reason. `#nosec G115 --
limit is clamped above to a bound far inside int32` is useful; a bare `#nosec`
is a hole with no explanation attached.

## Why Biome and not ESLint

ESLint is a linter plus a plugin ecosystem. A generated project would acquire
a parser, a plugin, a config preset, and their transitive versions before it
had checked a single file, and each of those is a thing to keep current.

Biome is one dependency and a configuration a reader can hold in their head.
It does not cover Svelte markup, which is why `svelte-check` is in the table
above rather than replaced by it.

Biome's formatter is **off**. Two formatters disagreeing over the same files
is worse than one, and this project has adopted neither — so Biome lints and
leaves layout alone.

## Why golangci-lint is a version pin

`make lint` requires the exact version this project is configured for, and
refuses a different one rather than running it. A linter that changes its
findings between two developers' machines produces an argument about whether
the code is wrong, and the version is the cheapest way not to have it.

It is not a `go.mod` tool directive, for the reason
[ADR 0008](adr/0008-toolchain-and-dependencies.md) gives about measured
`go.sum` growth. Install it with the command `make lint` prints.

## What linting the generated project found

The generated application had never been linted beyond `go vet`. Turning
these on produced twenty-nine Go findings and two hundred and three frontend
ones, and working through them changed real code rather than only
configuration:

- A tree walk that read files by path inside a `WalkDir` callback — the
  symlink TOCTOU shape — now walks through an `os.Root`.
- An unchecked `Close`, a non-null assertion in production Svelte, and a
  handful of unused test fixtures.
- A `%v` on an error that should have been `%w`, in three places.

And one that went the other way, which is the more interesting result.
`errorlint` asked for `%w` in `httpjson.Decode`, and taking the suggestion
made a **truncated** request body indistinguishable from an **absent** one:
`encoding/json` returns `io.EOF` for input that ends early, and this package's
callers use `errors.Is(err, io.EOF)` to mean "there was no body", which is a
different status code. The package's own fuzz test caught it. The fix keeps
the decoder's message as text and deliberately keeps its identity out of the
chain.

A linter's suggestion is a hypothesis. The tests are what decide.
