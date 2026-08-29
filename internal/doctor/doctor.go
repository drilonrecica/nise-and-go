package doctor

import (
	"context"
	"time"
)

// Options configures Run.
type Options struct {
	// Runner executes the external tools each check invokes. Nil selects
	// the real Runner (NewRunner()); tests pass a fake.
	Runner Runner
	// Timeout bounds each individual tool invocation. Zero or negative
	// selects DefaultTimeout.
	Timeout time.Duration
	// WorkDir is the directory Run treats as "here" when searching for a
	// project recipe — normally the process's current working directory.
	WorkDir string
	// NiseVersion is the version of the currently running nise build,
	// compared against a found recipe's recorded cliVersion. Typically
	// internal/version.Get().Version.
	NiseVersion string
}

// Run evaluates every doctor check and returns the full report, in a fixed
// order: the tools the golden profile needs, in the order a developer sets
// them up (Go, Node, pnpm, a container runtime, then the pinned Go-tool
// generators), followed by the two recipe checks. The order never depends
// on map iteration or any other non-deterministic source (constraints.md
// §5), so two runs against the same environment always report checks in
// the same sequence.
//
// Generator tools run only outside generated projects. Inside one they
// report StatusSkipped: sqlc and Goose are pinned but may require a
// network-backed tool/library build, while oapi-codegen is not pinned there
// yet. Explicit build, test, and generator commands are the user-authorized
// execution boundary.
//
// Run never returns an error itself: a tool being missing or too old is
// reported as a failing Check, not a Go error, so the caller (internal/
// cli/doctor.go) can render the full report either way and decide the
// process exit code from Report.Failed().
func Run(ctx context.Context, opts Options) Report {
	r := opts.Runner
	if r == nil {
		r = NewRunner()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// insideGeneratedProject reflects only whether a nise.json was found
	// at or above WorkDir, not whether it goes on to parse successfully:
	// the reason the generator-tool checks don't apply here (ADR 0009)
	// doesn't depend on the recipe's own validity, only on being inside a
	// generated project at all. A permission error walking up is treated
	// as "not a generated project" — the conservative default, since it
	// keeps the generator-tool checks meaningful outside a project, which
	// is where they matter most — and is separately surfaced by the
	// "recipe" check itself.
	_, insideGeneratedProject, _ := findRecipeRoot(opts.WorkDir)

	checks := []Check{
		checkGo(ctx, r, timeout),
		checkNode(ctx, r, timeout),
		checkPnpm(ctx, r, timeout),
		checkContainerRuntime(ctx, r, timeout),
		checkSqlc(ctx, r, timeout, insideGeneratedProject),
		checkGoose(ctx, r, timeout, insideGeneratedProject),
		checkOapiCodegen(ctx, r, timeout, insideGeneratedProject),
	}

	recipeCheck, rec := checkRecipe(opts.WorkDir)
	checks = append(checks, recipeCheck, checkRecipeCompatibility(rec, opts.NiseVersion))

	return Report{Checks: checks}
}
