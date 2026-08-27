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

	checks := []Check{
		checkGo(ctx, r, timeout),
		checkNode(ctx, r, timeout),
		checkPnpm(ctx, r, timeout),
		checkContainerRuntime(ctx, r, timeout),
		checkSqlc(ctx, r, timeout),
		checkGoose(ctx, r, timeout),
		checkOapiCodegen(ctx, r, timeout),
	}

	recipeCheck, rec := checkRecipe(opts.WorkDir)
	checks = append(checks, recipeCheck, checkRecipeCompatibility(rec, opts.NiseVersion))

	return Report{Checks: checks}
}
