package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Minimum versions pinned in docs/toolchain.md. These are the golden
// profile's floor, not the exact verified patch version: toolchain.md is
// explicit that no older 22.x Node has been verified (so 22.22.2 is the
// floor, not a ceiling) and that Go's own directive is "go 1.26.0" (the
// verified 1.26.5 toolchain is a specific installation of that same
// line, not a separate requirement). They are declared once, here, as
// unexported package-level values — not exported, not mutated after
// initialization, and not read from any external source — the same
// pattern dispatch.go's own reservedFlagNames and human.go's
// spinnerFrames already use for fixed configuration data; this is not
// the mutable global state constraints.md §3 rules out.
var (
	minGoVersion   = mustParseVersion("1.26.0")
	minNodeVersion = mustParseVersion("22.22.2")
	minPnpmVersion = mustParseVersion("10.33.0")
)

func mustParseVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(fmt.Sprintf("doctor: invalid pinned version literal %q: %v", s, err))
	}
	return v
}

// checkGo verifies `go version` resolves and is at least minGoVersion.
func checkGo(ctx context.Context, r Runner, timeout time.Duration) Check {
	return checkMinVersion(ctx, r, timeout, minVersionSpec{
		name:    "go",
		command: "go",
		args:    []string{"version"},
		min:     &minGoVersion,
		install: "Install Go " + minGoVersion.String() + " or newer from https://go.dev/dl/. GOTOOLCHAIN=local means nise never downloads one for you.",
	})
}

// checkNode verifies `node --version` resolves and is at least
// minNodeVersion.
func checkNode(ctx context.Context, r Runner, timeout time.Duration) Check {
	return checkMinVersion(ctx, r, timeout, minVersionSpec{
		name:    "node",
		command: "node",
		args:    []string{"--version"},
		min:     &minNodeVersion,
		install: "Install Node.js 22 LTS (>= " + minNodeVersion.String() + ") from https://nodejs.org/, or via nvm.",
	})
}

// checkPnpm verifies `pnpm --version` resolves and is at least
// minPnpmVersion.
func checkPnpm(ctx context.Context, r Runner, timeout time.Duration) Check {
	return checkMinVersion(ctx, r, timeout, minVersionSpec{
		name:    "pnpm",
		command: "pnpm",
		args:    []string{"--version"},
		min:     &minPnpmVersion,
		install: "Enable pnpm " + minPnpmVersion.String() + " via Corepack: `corepack enable && corepack use pnpm@" + minPnpmVersion.String() + "`.",
	})
}

// checkSqlc, checkGoose, and checkOapiCodegen verify their respective tool
// resolves through `go tool <name>` and prints a parsable version —
// docs/toolchain.md pins these three by go.mod `tool` directive rather
// than by a specific version number on that page — the exact pinned
// version lives in go.mod/go.sum, which this package does not parse (that
// would be reimplementing what `go tool` itself already resolves) — so
// there is no minimum version to compare against here, only "does it
// resolve and run".
//
// sqlc is pinned in generated projects from M3-002 onward, but doctor still
// does not invoke it there: Go may download and build a missing tool, which
// would violate doctor's no-implicit-network contract. The explicit
// sqlc-compile/generate commands are the user-authorized execution boundary.
func checkSqlc(ctx context.Context, r Runner, timeout time.Duration, insideGeneratedProject bool) Check {
	spec := minVersionSpec{
		name:    "sqlc",
		command: "go",
		args:    []string{"tool", "sqlc", "version"},
		min:     nil,
		install: "Run `go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc` from the repository root.",
	}
	if insideGeneratedProject {
		return Check{
			Name:     spec.name,
			Status:   StatusSkipped,
			Found:    "declared by this project's go.mod but not executed implicitly",
			Required: "run `make sqlc-compile` explicitly to resolve and verify the pinned local sqlc tool",
		}
	}
	return checkMinVersion(ctx, r, timeout, spec)
}

func checkGoose(ctx context.Context, r Runner, timeout time.Duration, insideGeneratedProject bool) Check {
	return checkGeneratorTool(ctx, r, timeout, insideGeneratedProject, minVersionSpec{
		name:    "goose",
		command: "go",
		args:    []string{"tool", "goose", "--version"},
		min:     nil,
		install: "Run `go get -tool github.com/pressly/goose/v3/cmd/goose` from the repository root.",
	})
}

func checkOapiCodegen(ctx context.Context, r Runner, timeout time.Duration, insideGeneratedProject bool) Check {
	return checkGeneratorTool(ctx, r, timeout, insideGeneratedProject, minVersionSpec{
		name:    "oapi-codegen",
		command: "go",
		args:    []string{"tool", "oapi-codegen", "-version"},
		min:     nil,
		install: "Run `go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` from the repository root.",
	})
}

// checkGeneratorTool routes a future go.mod-`tool`-pinned generator check
// (currently goose/oapi-codegen) based on whether doctor is running inside a
// generated project. Inside a generated project it never even invokes
// spec.command: there is nothing to run yet, by design, so a StatusFail
// (and its "add a tool directive" remedy, which is actively wrong advice
// for an application's own go.mod) would misreport a healthy project as
// broken. Outside a generated project — i.e. inside the Nise framework
// repository itself, where these tools are genuinely pinned and
// genuinely required — behavior is unchanged from before: checkMinVersion
// runs the tool and reports StatusFail if it is missing or unparsable.
func checkGeneratorTool(ctx context.Context, r Runner, timeout time.Duration, insideGeneratedProject bool, spec minVersionSpec) Check {
	if insideGeneratedProject {
		return Check{
			Name:   spec.name,
			Status: StatusSkipped,
			Found:  "not declared by this project's go.mod",
			Required: spec.name + " is only required inside the Nise framework repository itself; " +
				"a generated project does not declare it yet — this generator tool arrives with its later M3/M4 integration task",
		}
	}
	return checkMinVersion(ctx, r, timeout, spec)
}

// minVersionSpec describes one "run a command, parse a version, optionally
// compare against a floor" check.
type minVersionSpec struct {
	// name is the Check's Name and the label used in Required/Remedy text.
	name string
	// command and args are passed to Runner.Run verbatim — see
	// Runner.Run's doc comment on why this is safe despite being dynamic.
	command string
	args    []string
	// min is the minimum acceptable version, or nil when this check only
	// verifies the tool resolves and prints a parsable version (the three
	// go-tool-pinned generators; see their doc comments above).
	min *Version
	// install is the exact recovery action shown when the tool is
	// missing, unresponsive, or older than min.
	install string
}

// checkMinVersion runs spec's command, parses its output as a Version, and
// compares it against spec.min when one is set. It never reports
// StatusOK without having actually run the command and parsed a version
// out of its output — the "honest status" requirement this whole package
// exists to satisfy.
func checkMinVersion(ctx context.Context, r Runner, timeout time.Duration, spec minVersionSpec) Check {
	required := requiredText(spec)

	out, err := r.Run(ctx, timeout, spec.command, spec.args...)
	if err != nil {
		return Check{
			Name:     spec.name,
			Status:   StatusFail,
			Found:    err.Error(),
			Required: required,
			Remedy:   spec.install,
		}
	}

	v, err := ParseVersion(out)
	if err != nil {
		return Check{
			Name:     spec.name,
			Status:   StatusFail,
			Found:    fmt.Sprintf("ran %s but could not find a version number in its output: %q", spec.command, strings.TrimSpace(out)),
			Required: required,
			Remedy:   spec.install,
		}
	}

	if spec.min != nil && !v.AtLeast(*spec.min) {
		return Check{
			Name:     spec.name,
			Status:   StatusFail,
			Found:    spec.name + " " + v.String(),
			Required: required,
			Remedy:   "Upgrade " + spec.name + " to " + spec.min.String() + " or newer. " + spec.install,
		}
	}

	return Check{
		Name:     spec.name,
		Status:   StatusOK,
		Found:    spec.name + " " + v.String(),
		Required: required,
	}
}

func requiredText(spec minVersionSpec) string {
	if spec.min == nil {
		return spec.name + ", any version resolvable via `go tool`"
	}
	return spec.name + " " + spec.min.String() + " or newer"
}

// checkContainerRuntime verifies either Docker or Podman is present: the
// golden profile's development services run in a container, and either
// runtime satisfies that (see docs/toolchain.md's Containers section,
// which pins no specific version of either). It never assumes the binary
// named "docker" actually is Docker: on many systems `docker` is a Podman
// shim, so the runtime's own self-reported name in its --version output
// (not the binary name doctor happened to invoke) is what gets reported.
func checkContainerRuntime(ctx context.Context, r Runner, timeout time.Duration) Check {
	const required = "Docker or Podman"
	const remedy = "Install Docker (https://docs.docker.com/get-docker/) or Podman (https://podman.io/getting-started/installation) and ensure it is on PATH."

	if out, err := r.Run(ctx, timeout, "docker", "--version"); err == nil {
		if v, perr := ParseVersion(out); perr == nil {
			kind := classifyContainerRuntime(out)
			found := kind + " " + v.String()
			if kind != "docker" {
				found += fmt.Sprintf(" (invoked as \"docker\", which is a %s shim on this machine)", kind)
			}
			return Check{Name: "container-runtime", Status: StatusOK, Found: found, Required: required}
		}
	}

	if out, err := r.Run(ctx, timeout, "podman", "--version"); err == nil {
		if v, perr := ParseVersion(out); perr == nil {
			kind := classifyContainerRuntime(out)
			return Check{Name: "container-runtime", Status: StatusOK, Found: kind + " " + v.String(), Required: required}
		}
	}

	return Check{
		Name:     "container-runtime",
		Status:   StatusFail,
		Found:    "neither \"docker\" nor \"podman\" responded on PATH",
		Required: required,
		Remedy:   remedy,
	}
}

// classifyContainerRuntime reports which runtime actually produced output,
// by looking at what the tool called itself rather than which binary name
// doctor invoked to get there.
func classifyContainerRuntime(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "podman"):
		return "podman"
	case strings.Contains(lower, "docker"):
		return "docker"
	default:
		return "unknown container runtime"
	}
}
