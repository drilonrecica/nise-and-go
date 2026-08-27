# Observability

`runtime/logging` ([ADR 0011](adr/0011-runtime-public-api.md)) is how a generated application logs: structured JSON in production, a concise readable format locally, request and correlation IDs, and central redaction. This page documents the current, implemented behavior — it is not a design proposal.

`runtime/logging` is built directly on the standard library's `log/slog` and imports nothing else. It does not import `runtime/config`, and must not — see ADR 0011's import rules. An application wires a `runtime/config` value (for example, the configured environment or log level) into a `runtime/logging` constructor itself, at the call site; the two packages have no dependency on each other.

This package never establishes a global logger and never calls `slog.SetDefault`. `NewJSONHandler` and `NewTextHandler` return a `slog.Handler`; the caller wraps it with `slog.New` and decides what becomes of it.

## Log format contract

### Production: JSON

```go
handler := logging.NewJSONHandler(os.Stdout, logging.HandlerOptions{
    Level: slog.LevelInfo,
})
logger := slog.New(handler)
```

Each record is one JSON object per line, in the standard `log/slog` JSON shape: `time`, `level`, `msg`, then attributes in the order they were added. For example:

```go
logger.Info("user signed in", "user_id", "u_123", "password", "hunter2")
```

produces:

```json
{"time":"2026-08-27T12:30:45Z","level":"INFO","msg":"user signed in","user_id":"u_123","password":"[REDACTED]"}
```

`password` is redacted because every attribute passes through central redaction before it reaches the JSON encoder — see [Redaction](#redaction).

### Local development: readable text

```go
handler := logging.NewTextHandler(os.Stderr, logging.TextOptions{
    HandlerOptions: logging.HandlerOptions{Level: slog.LevelDebug},
    Color:          true,
})
logger := slog.New(handler)
```

Each record is one line: a millisecond-precision time, the level aligned to a fixed 5-character width, the message, then attributes as `key=value` (quoted with Go quoting rules when a value contains a space, `"`, `=`, or a backslash), in the order they were added — across chained `.With(...)` calls, `.WithGroup(...)` calls, and the attributes passed to the log call itself. A grouped attribute's key is the dot-joined path of its enclosing groups.

```
12:30:45.000 INFO  user signed in user_id=u_123 password=[REDACTED]
```

Attribute order is always insertion order, never map iteration, specifically so tests can assert on exact output.

`TextOptions.Color` wraps the level in an ANSI colour code (faint for debug, cyan for info, yellow for warn, red for error) when `true`, and never otherwise. **This package does not detect whether its writer is a terminal.** That decision — checking `isatty`, honoring `NO_COLOR`, and so on — belongs to the CLI, which is the part of Nise that knows about terminals; `runtime/logging` only ever does what the caller explicitly asks with `Color`.

### No request or response body logging

Neither handler, nor `Middleware`, ever logs an HTTP request or response body, or any part of one, by default. There is no method in this package that accepts one. An application that needs to log a body must do so explicitly and is responsible for not doing so with anything it wouldn't otherwise log.

## Request and correlation IDs

Two distinct identifiers, each with its own `context.Context` key and accessor pair:

| ID | Identifies | Accessors | Log field |
|---|---|---|---|
| Request ID | One request to this process | `logging.WithRequestID`, `logging.RequestID` | `request_id` |
| Correlation ID | A logical operation across process, request, and job boundaries | `logging.WithCorrelationID`, `logging.CorrelationID` | `correlation_id` |

A request ID is scoped to a single hop. A correlation ID is what lets a browser request, the background job it enqueues, and any downstream call the job makes all point back to the same operation in your logs, even though each has its own request ID.

`logging.NewID()` generates a fresh ID directly — 16 bytes from `crypto/rand`, encoded with unpadded URL-safe base64 (22 characters, no `=`, `/`, or `+`) — for use outside an HTTP request, such as a job's correlation ID.

### `Middleware` and inbound header trust

```go
mw := logging.Middleware(logger, logging.MiddlewareOptions{
    TrustInbound: false, // the default; set true only behind a trusted ingress
})
router.Use(mw)
```

For every request, `Middleware` resolves a request ID and a correlation ID from `X-Request-Id` and `X-Correlation-Id` (override with `MiddlewareOptions.RequestIDHeader` / `CorrelationIDHeader`), stores both in the request's context, attaches both as attributes to a request-scoped logger stored alongside them (retrieve it with `logging.FromContext`), and sets both as response headers.

**Accepting an inbound ID from an untrusted client is a security decision, not a convenience — this package treats it as opt-in and always validates it.** `MiddlewareOptions.TrustInbound` defaults to `false`: with it unset, both IDs are always freshly generated with `NewID`, regardless of what a client sends. Setting it `true` is appropriate only behind an ingress that itself sanitizes or generates these headers (a load balancer, an internal service-mesh hop) — not directly in front of a browser or another untrusted client.

Even with `TrustInbound: true`, an inbound value is only accepted if it passes `logging.ValidID`: non-empty, at most `MaxIDLength` (128) bytes, and composed only of ASCII letters, digits, `-`, and `_`. Anything else — regardless of `TrustInbound` — is discarded in favor of a freshly generated ID. This specifically defeats:

- **Newline injection (log forging).** A value like `"abc\nfake: admin logged in"` cannot be used to fabricate a second log line.
- **ANSI escape sequences (terminal injection).** A value containing `\x1b[...` cannot manipulate a terminal that later displays the log.
- **Oversized values.** A 10 KB header value is rejected outright by the length bound, before it can bloat every log line for the life of the request.
- **Empty and non-ASCII values.** Both are rejected; an ID must be exactly the safe, portable shape `ValidID` describes.

`Middleware` applies this same validation uniformly to both the request ID header and the correlation ID header — there is no header for which trust or validation is weaker.

## Redaction

### The guarantee

`NewJSONHandler` and `NewTextHandler` both wrap their output in `NewRedactingHandler` automatically. This is not something a caller can forget: using either constructor gets you redaction, not something you opt into per call site. `NewRedactingHandler` is also exported directly, for wrapping a different base `slog.Handler`.

Redaction replaces the value of any attribute whose **key** matches a deny set — case-insensitive, exact match — with a placeholder (`[REDACTED]` by default, or `RedactOptions.Placeholder`). The built-in deny set (`logging.DefaultDenyKeys()`) is:

```
password  passwd  secret  token  authorization  cookie  set-cookie
api_key  apikey  session  csrf  private_key  credential
```

`RedactOptions.ExtraDenyKeys` extends this list for application-specific field names — for example `access_token`, which does not literally match the default `token` entry because matching is exact, not substring or prefix.

Redaction is applied on every `WithAttrs` and `Handle` call, so it survives:

- **Arbitrary nesting.** A deny-set key three `WithGroup` calls deep, or nested inside a `slog.Group` value, is still redacted; the handler resolves and recurses into every group.
- **A `slog.LogValuer`.** The handler calls `slog.Value.Resolve()` before deciding whether a value is a group to recurse into, so a type like:

  ```go
  type creds struct{ Username, Password string }
  func (c creds) LogValue() slog.Value {
      return slog.GroupValue(
          slog.String("username", c.Username),
          slog.String("password", c.Password),
      )
  }
  logger.Info("login", "creds", creds{...})
  ```

  still has `creds.password` redacted, even though the outer attribute's own key (`creds`) is not deny-listed. A naive implementation that only inspected the outer key would miss this.

### The limit — read this before relying on redaction

**Redaction is key-based. It has no way to see inside a message string.** `logger.Info("connecting with password=hunter2")` is not redacted by anything in this package, because `hunter2` is not the value of an attribute with a deny-listed key — it is a substring of the message. Nothing here scans message text for secret-shaped content. If your code interpolates a secret into a format string, it will be logged in full, in full view of anyone who can read the log. This is a call-site discipline problem `runtime/logging` cannot solve for you: always pass a sensitive value as an attribute, never as part of the message.

The deny-set match is exact, not substring or prefix. A field named `db_password` is not redacted by the default set (only a field literally named `password`, `secret`, etc. is); add it via `RedactOptions.ExtraDenyKeys`.

### `Redact` for connection strings and URLs

`logging.Redact(raw string) string` handles the specific case a key-based deny set cannot: a secret embedded inside a single string value, such as a database connection URI, rather than carried as its own attribute.

```go
logging.Redact("postgres://appuser:s3cr3t@db.internal:5432/appdb?sslmode=require")
// => "postgres://db.internal:5432/appdb?sslmode=require"

logging.Redact("smtp://mailer:hunter2@smtp.example.com:587")
// => "smtp://smtp.example.com:587"
```

It parses `raw` as an absolute URL, removes userinfo (both username and password) entirely, and replaces any query parameter whose name is in `DefaultDenyKeys` with the placeholder. Its limits:

- It only recognizes deny-set-shaped query parameter names. A credential passed under a different name is not removed.
- It cannot parse a connection string that is not URL-shaped, such as PostgreSQL's space-separated `key=value` DSN syntax (`host=localhost user=postgres password=hunter2`).
- When `raw` does not parse as an absolute URL with both a scheme and a host, `Redact` cannot verify it holds no secret and fails closed: it returns a fixed placeholder rather than risk returning unredacted, potentially secret-bearing text.

## Import direction

`runtime/logging` imports only the standard library. It does not import `runtime/config`, `internal/`, `cmd/`, `templates/`, `test/`, or `examples/`. `runtime/lifecycle` is the only package permitted to import `runtime/logging` (ADR 0011).
