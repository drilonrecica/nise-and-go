# Configuration

`runtime/config` ([ADR 0011](adr/0011-runtime-public-api.md)) is how a generated application reads its configuration: typed values from the process environment, secrets through file indirection, and a fail-closed check that refuses to start an unsafe production deployment. This page documents the current, implemented behavior — it is not a design proposal.

## Design: explicit combinators

An application declares configuration as a plain Go struct and fills it in with a small set of combinator methods on a `Loader` — `String`, `Int`, `Bool`, `Duration`, `Environment`, and `Secret` — rather than a struct-tag decoder. Each field's source and default are visible at the call site:

```go
l := config.NewLoader()
cfg := AppConfig{
    Env:      l.Environment("APP_ENV", config.Default("development")),
    Port:     l.String("PORT", config.Default("8080")),
    Debug:    l.Bool("DEBUG", config.Default("false")),
    Timeout:  l.Duration("REQUEST_TIMEOUT", config.Default("30s")),
    DBPassword: l.Secret("DB_PASSWORD"),
}
if err := l.Err(); err != nil {
    return AppConfig{}, err
}
```

This package deliberately does not use reflection-driven struct tags. A struct tag string is invisible to the compiler and to `go vet`; a call to `l.String(...)` is not. The trade-off is one line of code per field instead of one tag per field, which is a small cost for the number of settings a generated application has.

## Source precedence

Every field has up to three possible sources, consulted in this order:

1. The plain environment variable, `FOO`.
2. For secrets only (`Loader.Secret`): file indirection, `FOO_FILE`, whose contents become the value.
3. The compiled-in default passed via `config.Default(...)`, if the field was loaded with one.

A field loaded without `config.Default(...)` is **required**: if no source provides a value, loading it fails. Failures from every field loaded through the same `Loader` accumulate; `Loader.Err()` returns them together as a single joined error, so an application reports every missing or malformed variable in one failed startup instead of one per restart.

**Setting both `FOO` and `FOO_FILE` is always an error.** It is never treated as a preference for one source over the other. An operator who has both set almost certainly changed one and forgot to remove the other; guessing which one they meant would silently do the wrong thing in exactly the situation where correctness matters most.

**`runtime/config` never reads a `.env` file.** Loading one is a local development convenience owned by `nise dev`, not a production runtime behavior — the runtime's behavior must not depend on which files happen to be sitting next to the binary.

## The `_FILE` convention

Any secret loaded with `Loader.Secret("FOO")` accepts `FOO_FILE` as an alternative source, naming a file whose contents become the value. This is the standard convention for feeding secrets from an orchestrator's mounted-file secret store without putting the value on the process environment.

Rules for a `_FILE` value:

- The file must exist, be readable, and not be a directory.
- Exactly one trailing newline is stripped: a trailing `\r\n` or `\n`, but nothing else. Interior newlines are preserved.
- The file must not be empty, before or after that newline is stripped — a file containing only a newline is treated as an empty secret.
- The file must not exceed `config.MaxSecretFileSize` (1 MiB). This exists so a misconfigured path cannot exhaust memory by pointing at an unrelated large file.
- If the file is readable by group or other on a Unix platform, loading still succeeds, but `Loader.Warnings()` reports it. This is a warning, not a load failure: a loose file permission is worth fixing but is not itself proof the secret has already leaked, so refusing to start over it would be disproportionate. The check is skipped entirely on Windows.

Every error and warning path names the environment variable and the file path, and states what to do — never the file's contents.

## The `Secret` type and redaction guarantee

`config.Secret` wraps any sensitive value. It is unprintable through every formatting and serialization path Go offers:

| Path | Behavior |
|---|---|
| `fmt.Sprintf("%v", s)`, `"%s"` | Redaction placeholder via `String()` |
| `fmt.Sprintf("%q", s)` | Redaction placeholder, quoted |
| `fmt.Sprintf("%#v", s)` | Redaction placeholder via `GoString()` |
| `json.Marshal(s)` | Redaction placeholder via `MarshalJSON()` |
| `encoding.TextMarshaler` callers | Redaction placeholder via `MarshalText()` |
| `slog.Logger` calls with `s` as a field value | Redaction placeholder via `LogValue()` |

The only way to obtain the real value is the explicit `Secret.Reveal()` method. Applications should call `Reveal()` only where the value is genuinely needed — for example, to open a database connection — never to log, print, or include it in an error.

`config.NewSecret(value string) config.Secret` wraps a sensitive value obtained outside `Loader` (for example one derived at runtime) under the same guarantee.

## Environment

`config.Environment` is a closed, typed enum with exactly three values: `Development`, `Test`, and `Production`. `config.ParseEnvironment` — and `Loader.Environment`, which calls it — reject any other string as an error. There is no default and no case-insensitive match.

An unrecognized value fails closed rather than guessing, because either guess is unsafe: defaulting to `Development` could start a misconfigured deployment with production's safety checks disabled, and defaulting to `Production` could impose production-only requirements on a deployment nobody meant to be production. Refusing to start is the only option that does not depend on guessing correctly.

## Fail-closed production validation

`config.Validate(env, checks)` runs a caller-supplied function of checks, but only when `env == config.Production`; for `Development` and `Test` it returns `nil` without calling `checks` at all. Inside `checks`, an application calls `Validator.Check(ok bool, name, problem, action string)` for each production safety requirement it has, and `Validator.RequireSecretSet(name, secret)` for each secret that must not be empty in production. Typical checks:

```go
err := config.Validate(cfg.Env, func(v *config.Validator) {
    v.RequireSecretSet("SESSION_SECRET", cfg.SessionSecret)
    v.RequireSecretSet("DB_PASSWORD", cfg.DBPassword)
    v.Check(!cfg.Debug, "DEBUG", "debug mode is enabled in production", "set DEBUG=false")
    v.Check(cfg.CookieSecure, "COOKIE_SECURE", "insecure cookie settings are enabled in production", "set COOKIE_SECURE=true")
    v.Check(cfg.BindAddr != "0.0.0.0" || cfg.AllowPublicBind,
        "BIND_ADDR", "bound to a public address without an explicit opt-in",
        "set BIND_ADDR to a private address or ALLOW_PUBLIC_BIND=true")
    v.Check(cfg.DatabaseURL.IsSet(), "DATABASE_URL", "no database connection is configured", "set DATABASE_URL")
})
if err != nil {
    // refuse to start; err lists every violation found, not just the first
}
```

Every violation `checks` records is collected; `Validate` returns them together as a single joined error, so a failed startup tells an operator everything to fix at once rather than one problem per restart. No error message produced by this package ever contains a secret's value — only the variable name, what was wrong, and what to do.

## Worked example

```go
package config

import (
    "time"

    rtconfig "github.com/drilonrecica/nise-and-go/runtime/config"
)

type AppConfig struct {
    Env             rtconfig.Environment
    Port            string
    Debug           bool
    RequestTimeout  time.Duration
    CookieSecure    bool
    BindAddr        string
    AllowPublicBind bool
    DatabaseURL     rtconfig.Secret
    SessionSecret   rtconfig.Secret
}

func Load() (AppConfig, error) {
    l := rtconfig.NewLoader()
    cfg := AppConfig{
        Env:             l.Environment("APP_ENV", rtconfig.Default("development")),
        Port:            l.String("PORT", rtconfig.Default("8080")),
        Debug:           l.Bool("DEBUG", rtconfig.Default("false")),
        RequestTimeout:  l.Duration("REQUEST_TIMEOUT", rtconfig.Default("30s")),
        CookieSecure:    l.Bool("COOKIE_SECURE", rtconfig.Default("true")),
        BindAddr:        l.String("BIND_ADDR", rtconfig.Default("127.0.0.1")),
        AllowPublicBind: l.Bool("ALLOW_PUBLIC_BIND", rtconfig.Default("false")),
        DatabaseURL:     l.Secret("DATABASE_URL"),
        SessionSecret:   l.Secret("SESSION_SECRET"),
    }
    if err := l.Err(); err != nil {
        return AppConfig{}, err
    }

    if err := rtconfig.Validate(cfg.Env, func(v *rtconfig.Validator) {
        v.RequireSecretSet("SESSION_SECRET", cfg.SessionSecret)
        v.RequireSecretSet("DATABASE_URL", cfg.DatabaseURL)
        v.Check(!cfg.Debug, "DEBUG", "debug mode is enabled in production", "set DEBUG=false")
        v.Check(cfg.CookieSecure, "COOKIE_SECURE", "insecure cookie settings are enabled in production", "set COOKIE_SECURE=true")
        v.Check(cfg.BindAddr != "0.0.0.0" || cfg.AllowPublicBind,
            "BIND_ADDR", "bound to a public address without an explicit opt-in",
            "set BIND_ADDR to a private address or ALLOW_PUBLIC_BIND=true")
    }); err != nil {
        return AppConfig{}, err
    }

    return cfg, nil
}
```

Running with `APP_ENV=production` but no `SESSION_SECRET` or `DATABASE_URL` set, and `DEBUG=true`, fails startup with one error naming all three problems — not just the first one encountered.

## Generated PostgreSQL pool settings

The golden-profile application requires `DATABASE_URL` (or
`DATABASE_URL_FILE`) and applies fixed conservative pgxpool defaults.
`DATABASE_PASSWORD` (or `DATABASE_PASSWORD_FILE`) may supply the
password separately; when set, its exact value overrides URL userinfo. This
is the safe form for deployment systems interpolating a secret that may
contain URL-reserved characters.

| Variable | Default | Constraint |
|---|---:|---|
| `DB_MAX_CONNS` | `10` | 1 through 2,147,483,647 |
| `DB_MIN_CONNS` | `0` | 0 through `DB_MAX_CONNS` |
| `DB_MAX_CONN_LIFETIME` | `30m` | greater than zero |
| `DB_MAX_CONN_IDLE_TIME` | `5m` | greater than zero and no longer than the maximum lifetime |
| `DB_HEALTH_CHECK_PERIOD` | `30s` | greater than zero |
| `DB_CONNECT_TIMEOUT` | `5s` | greater than zero |
| `DB_SLOW_QUERY_THRESHOLD` | `250ms` | greater than zero |

The maximum is explicit instead of pgxpool's CPU-derived default: adding CPU
to an application replica must not silently consume more PostgreSQL
connections. `DB_MIN_CONNS=0` also avoids holding open idle connections
for small deployments. The application proves one connection during startup,
registers the pool ping with readiness, reports bounded pool metrics, and
closes the pool during shutdown.

## Generated pagination cursor settings

The golden-profile application signs the opaque cursors its collections issue.
See [Pagination](pagination.md) for the wire contract and the rotation
procedure.

| Variable | Default | Constraint |
|---|---:|---|
| `CURSOR_SIGNING_KEY` | *(generated)* | `<id>:<base64 secret>`; identifier lowercase alphanumeric with `-`/`_`, secret at least 32 bytes |
| `CURSOR_RETIRED_KEYS` | *(none)* | comma-separated keys in the same form, identifiers distinct from each other and from the signing key |
| `CURSOR_TTL` | `1h` | greater than zero and at most `24h` |

Both key variables accept the `_FILE` indirection. An unset
`CURSOR_SIGNING_KEY` generates a random key for the process and emits a
configuration warning; production refuses to start on that path, because every
restart and every replica would then reject the others' cursors. There is no
built-in default key. `CURSOR_RETIRED_KEYS` without a signing key is an error:
a retired key verifies cursors but cannot issue them.

The slow-query threshold applies to pgx query, batch, and copy round trips.
Crossing it emits a bounded operation label, outcome, duration, threshold, and
available validated correlation IDs; SQL, arguments, copied rows, results, and
database errors are never logged. See
[Database query instrumentation](database-query-instrumentation.md).
