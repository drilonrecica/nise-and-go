package check

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"

	rtconfig "github.com/drilonrecica/nise-and-go/runtime/config"
	"github.com/drilonrecica/nise-and-go/runtime/lifecycle"
)

const invariantConfig = "An environment file the application would start from breaks no configuration rule runtime/ enforces unconditionally."

// secretFileSuffix is the suffix runtime/config's Loader.Secret recognizes:
// a value may be supplied either as NAME or as NAME_FILE, never as both.
const secretFileSuffix = "_FILE"

// envKeyPattern is the shape of an environment variable name. A line whose
// left-hand side does not match is not an assignment this parser claims to
// understand, and is reported as unparseable rather than guessed at.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkConfig validates a supplied environment file against the
// configuration rules the Nise runtime enforces on its own, without
// starting, building, or importing the application.
//
// # What this check deliberately does not do
//
// It does not run the application's own production validation. It cannot:
// that validation lives in internal/platform/config/config.go inside the
// generated project, which is application-owned (ADR 0003) and which the
// application is free to rewrite. Re-implementing the starter's version of
// it here — asserting DEBUG=false, LOG_FORMAT=json, or the
// BIND_ADDR/ALLOW_PUBLIC_BIND pairing — would enforce starter conformity on
// a file whose ownership model says the application decides, and would fail
// a correct project that replaced that policy with its own. Those three
// exclusions are recorded, with this reasoning, in docs/commands/check.md.
//
// What is left is exactly the set of rules the application cannot opt out
// of, because runtime/config and runtime/lifecycle apply them before any
// application code runs: the typed environment is a closed set, the process
// mode is a closed set, and a secret may be supplied as NAME or NAME_FILE
// but never as both. Each is checked through the runtime's own parser, so a
// change to what the runtime accepts changes what this check accepts,
// automatically.
//
// That last sentence is why this file imports runtime/config and
// runtime/lifecycle at all, and it is the whole justification for the
// import: a second copy of ParseEnvironment's and ParseMode's closed value
// sets would drift, and this check would then validate something other than
// what the process will actually accept. ADR 0011's import rules permit
// internal/ to import runtime/ on exactly that ground and no other — reusing
// a rule the runtime enforces unconditionally — and name this import as the
// only current instance. Adding another one means adding its justification
// to that list. It is not a general licence to build CLI code on runtime
// types; see internal/cli/clierr/redact.go for a case where duplication
// remains the right answer.
func checkConfig(envFile string) Check {
	if envFile == "" {
		return skip(CheckConfig, invariantConfig,
			"no --env-file was given, and there is no file to guess at: validating a developer's local .env as though it were a production one would report on the wrong file")
	}

	data, err := os.ReadFile(envFile) // #nosec G304 -- envFile is a path the user typed on the command line; reading it is the entire purpose of the flag.
	if err != nil {
		return fail(CheckConfig, invariantConfig,
			fmt.Sprintf("could not read the environment file %s (%s)", envFile, err),
			"Pass --env-file a path this user can read.",
		)
	}

	values, malformed := parseEnvFile(string(data))

	violations := append([]string(nil), malformed...)

	// The typed environment. runtime/config.ParseEnvironment defines a
	// closed set with no default and no fourth value; a process started
	// with anything else fails before it serves a request.
	appEnv, hasAppEnv := values["APP_ENV"]
	if hasAppEnv {
		if _, perr := rtconfig.ParseEnvironment(appEnv); perr != nil {
			violations = append(violations, "APP_ENV: "+perr.Error())
		}
	}

	// The process mode, same reasoning, through runtime/lifecycle's own
	// parser.
	if appMode, ok := values["APP_MODE"]; ok {
		if _, perr := lifecycle.ParseMode(appMode); perr != nil {
			violations = append(violations, "APP_MODE: "+perr.Error())
		}
	}

	// NAME and NAME_FILE together. runtime/config's Loader.Secret rejects
	// this unconditionally, in every environment, because the two disagree
	// about where the value comes from and silently preferring one would
	// hide the other.
	for _, name := range sortedKeys(values) {
		if !strings.HasSuffix(name, secretFileSuffix) {
			continue
		}
		base := strings.TrimSuffix(name, secretFileSuffix)
		if base == "" {
			continue
		}
		if _, both := values[base]; both {
			violations = append(violations, fmt.Sprintf(
				"%s and %s are both set; runtime/config accepts a secret from one or the other, never both", base, name))
		}
	}

	// Secret file permissions, when the named file happens to be present on
	// this machine. runtime/config warns at startup for a secret file
	// readable by group or other; finding it here means finding it before
	// the deployment does. A file that is not present locally is not a
	// finding: the path names the deployment host's filesystem, not this
	// one.
	violations = append(violations, secretFilePermissionViolations(values)...)

	if len(violations) > 0 {
		slices.Sort(violations)
		c := fail(CheckConfig, invariantConfig,
			fmt.Sprintf("%s breaks %d configuration rule(s): %s", envFile, len(violations), strings.Join(violations, "; ")),
			"Fix the listed variables in the environment file. Every rule above is enforced by runtime/config or runtime/lifecycle at startup, so a deployment reading this file would fail to start.",
		)
		c.Paths = []string{envFile}
		return c
	}

	described := "in every environment"
	if hasAppEnv {
		described = fmt.Sprintf("for APP_ENV=%s", appEnv)
	}
	c := pass(CheckConfig, invariantConfig, fmt.Sprintf(
		"%s: %d variable(s), and none breaks a rule runtime/config or runtime/lifecycle enforces %s",
		envFile, len(values), described,
	))
	c.Paths = []string{envFile}
	return c
}

// secretFilePermissionViolations reports every NAME_FILE value that names a
// file present on this machine and readable by group or other, mirroring
// runtime/config's own rule (see checkSecretFilePermissions there). It
// reports nothing on Windows, where the POSIX permission bits the rule
// relies on do not apply — the same exemption runtime/config makes.
func secretFilePermissionViolations(values map[string]string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	var out []string
	for _, name := range sortedKeys(values) {
		if !strings.HasSuffix(name, secretFileSuffix) {
			continue
		}
		path := values[name]
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if perm := info.Mode().Perm(); perm&0o044 != 0 {
			out = append(out, fmt.Sprintf(
				"%s names %q, which is readable by group or other (mode %04o); restrict it to the owner only", name, path, perm))
		}
	}
	return out
}

// parseEnvFile reads the KEY=VALUE form used by the generated .env.example
// and by every process manager that reads an env file. It returns the
// resolved values and a description of every line it could not parse.
//
// The dialect it accepts is deliberately the small one: blank lines and
// lines whose first non-space character is "#" are ignored; an optional
// leading "export " is allowed; a value wrapped in matching single or double
// quotes has them removed and is otherwise taken literally; an unquoted
// value ends at the first " #" (a trailing comment) and is trimmed. A
// repeated key takes its last value, matching how a shell sourcing the file
// would behave.
//
// It deliberately does not implement escape sequences, multi-line values, or
// variable interpolation. A line it cannot parse is reported, not guessed
// at: silently mis-reading a production environment file is a worse outcome
// than saying so.
func parseEnvFile(content string) (values map[string]string, malformed []string) {
	values = make(map[string]string)
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envKeyPattern.MatchString(key) {
			malformed = append(malformed, fmt.Sprintf("line %d is not a KEY=VALUE assignment", i+1))
			continue
		}
		values[key] = unquoteEnvValue(value)
	}
	return values, malformed
}

// unquoteEnvValue applies the value rules parseEnvFile documents.
func unquoteEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			return value[1 : len(value)-1]
		}
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

// sortedKeys returns m's keys sorted, so every message this file builds is
// in the same order on every run (Global Constraint 5).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
