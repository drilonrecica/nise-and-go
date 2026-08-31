package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/cli/output"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// Suite names, used both as suitePlan.Name and as the map keys
// selectedSuites returns, and printed verbatim in output.
const (
	suiteGo        = "go"
	suiteComponent = "component"
	suiteE2E       = "e2e"
)

// suiteOrder is the fixed order suites run in, regardless of which flags
// selected them: Go first (fastest feedback, no Node toolchain needed),
// then the browser-component suite, then end-to-end last (slowest,
// spins a real browser). Fixed order keeps two runs of the same
// selection comparable and never depends on flag parse order.
var suiteOrder = []string{suiteGo, suiteComponent, suiteE2E}

// componentScriptNames and e2eScriptNames are the frontend/package.json
// script names nise test looks for, in priority order, to decide whether
// the browser-component and end-to-end suites are present at all. Neither
// name is invented by nise test itself: both are read from the project's
// own package.json, exactly as the project declared them, so a project that
// renames or removes one is reporting its own shape rather than fighting
// this command.
//
// `test:component` comes first because a generated project defines it as
// "every component suite there is" — the Node modules and the real-browser
// components together. `test:unit` remains a fallback for a project that has
// only the fast half, which is the shape M6 shipped before browser mode
// existed and the shape an application may deliberately keep.
var (
	componentScriptNames = []string{"test:component", "test:unit"}
	e2eScriptNames       = []string{"test:e2e", "e2e"}
)

// suiteStatus is one of the values suiteResult.Status takes.
type suiteStatus string

const (
	statusPassed  suiteStatus = "passed"
	statusFailed  suiteStatus = "failed"
	statusSkipped suiteStatus = "skipped"
)

// suitePlan is what planning a suite decides: whether it can run at all,
// and if so, the exact command that runs it. Planning never invents a
// command a project did not declare — see planGoSuite and
// planPackageScriptSuite.
type suitePlan struct {
	Name       string
	Runnable   bool
	Argv       []string // Argv[0] is the executable.
	Dir        string   // Working directory for Argv.
	SkipReason string   // Set only when Runnable is false.
}

// describe renders p's command for human and JSON reporting, e.g.
// "go test ./cmd/... ./db/... ./internal/...".
func (p suitePlan) describe(extraArgs []string) string {
	return strings.Join(append(append([]string{}, p.Argv...), extraArgs...), " ")
}

// planGoSuite reports whether root looks like a Go project (it has a
// go.mod at its root — a generated Nise project always does, per
// docs/generated-application-layout.md) and, if so, the exact command
// docs/generated-application-layout.md's own "Go targets" section
// prescribes: frontend/ sits inside the same Go module, so a bare `./...`
// would also walk frontend/node_modules/.
//
// The package list is exactly the generated Makefile's GO_PACKAGES, and
// deliberately so: two commands claiming to run "the project's Go tests"
// must not run different sets. The generated db package owns the migration
// source/embed parity test, so omitting ./db/... would let deployed migration
// bytes drift from their reviewed SQL without the standard suite noticing.
func planGoSuite(root string) suitePlan {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return suitePlan{Name: suiteGo, SkipReason: "no go.mod found at the project root"}
	}
	return suitePlan{
		Name:     suiteGo,
		Runnable: true,
		Argv:     []string{"go", "test", "./cmd/...", "./db/...", "./internal/..."},
		Dir:      root,
	}
}

// packageJSON is the subset of frontend/package.json this package reads.
type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

// planPackageScriptSuite looks for a script named one of scriptNames, in
// order, in frontend/package.json, and reports the first one found as the
// suite's command (`pnpm --dir frontend run <script>`). It never falls
// back to guessing a command when no matching script exists — that suite
// is reported skipped, with the exact reason, rather than nise test
// inventing something the project never declared.
func planPackageScriptSuite(name, root string, scriptNames []string) suitePlan {
	pkgPath := filepath.Join(root, "frontend", "package.json")
	data, err := os.ReadFile(pkgPath) // #nosec G304 -- pkgPath is joined from this command's own root and fixed literals, never from unvalidated external input.
	if err != nil {
		return suitePlan{Name: name, SkipReason: "frontend/package.json not found"}
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return suitePlan{Name: name, SkipReason: fmt.Sprintf("frontend/package.json does not parse as JSON: %s", err)}
	}
	for _, script := range scriptNames {
		if _, ok := pkg.Scripts[script]; ok {
			return suitePlan{
				Name:     name,
				Runnable: true,
				Argv:     []string{"pnpm", "--dir", "frontend", "run", script},
				Dir:      root,
			}
		}
	}
	return suitePlan{
		Name: name,
		SkipReason: fmt.Sprintf("no %s script found in frontend/package.json (looked for: %s)",
			name, strings.Join(scriptNames, ", ")),
	}
}

// suiteResult is one suite's outcome, and also output.Result's JSON shape
// for one entry of testSummaryResult.Suites.
type suiteResult struct {
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Command         string  `json:"command,omitempty"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	ExitCode        *int    `json:"exitCode,omitempty"`
}

// lineForwarder is an io.Writer that splits whatever bytes a child
// process writes into lines and forwards each complete line to out.Line
// as it arrives — the mechanism behind "streams child output through
// unchanged in human mode" from this command's own docs: out.Line is a
// no-op in --json mode and when --quiet (docs/cli-output.md), so wiring a
// child's stdout/stderr through it is what keeps a JSON invocation's
// stdout free of interleaved raw child text without this file needing to
// know its own output mode at all. A trailing partial line (no final
// newline) is only forwarded once Flush is called after the child exits.
type lineForwarder struct {
	out output.Writer
	buf []byte
}

// Write implements io.Writer.
func (f *lineForwarder) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		i := bytes.IndexByte(f.buf, '\n')
		if i < 0 {
			break
		}
		f.out.Line(strings.TrimSuffix(string(f.buf[:i]), "\r"))
		f.buf = f.buf[i+1:]
	}
	return len(p), nil
}

// Flush forwards any buffered partial line. Call it once after the child
// exits: os/exec's own internal io.Copy goroutines stop at EOF without
// ever telling this writer the stream is done, so a trailing line with no
// final newline would otherwise never be forwarded at all.
func (f *lineForwarder) Flush() {
	if len(f.buf) > 0 {
		f.out.Line(string(f.buf))
		f.buf = nil
	}
}

// runSuite executes plan (if runnable) with extraArgs appended to its own
// command, streaming its combined stdout/stderr through out one line at a
// time as it runs. ctx governs cancellation: exec.CommandContext kills the
// child the moment ctx is done, which is exactly the mechanism that keeps
// a Ctrl-C from orphaning a running go test or pnpm process (see
// testCommand's signal.NotifyContext wiring).
func runSuite(ctx context.Context, plan suitePlan, extraArgs []string, out output.Writer) suiteResult {
	if !plan.Runnable {
		return suiteResult{Name: plan.Name, Status: string(statusSkipped), Reason: plan.SkipReason}
	}
	if err := ctx.Err(); err != nil {
		return suiteResult{Name: plan.Name, Status: string(statusSkipped), Reason: "interrupted before this suite started"}
	}

	argv := append(append([]string{}, plan.Argv...), extraArgs...)
	// #nosec G204 -- argv[0] is always one of this file's own fixed
	// literals ("go" or "pnpm"); argv[1:] is this file's own fixed flags
	// plus, for the Go suite only, args the user explicitly passed after
	// a literal "--" on the nise command line (docs/cli-output.md's own
	// documented `--` passthrough contract) — never unvalidated input
	// reaching an untraced path.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = plan.Dir
	stdout := &lineForwarder{out: out}
	stderr := &lineForwarder{out: out}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)
	stdout.Flush()
	stderr.Flush()

	status := statusPassed
	if runErr != nil {
		status = statusFailed
	}

	result := suiteResult{
		Name:            plan.Name,
		Status:          string(status),
		Command:         plan.describe(extraArgs),
		DurationSeconds: duration.Seconds(),
	}
	if cmd.ProcessState != nil {
		ec := cmd.ProcessState.ExitCode()
		result.ExitCode = &ec
	}
	return result
}

// selectedSuites resolves which suites --go/--component/--e2e select. With
// none of the three flags given, the default is the Go and
// browser-component suites — the fast, hermetic ones: they need no real
// browser session and no network, so they belong in a tight local dev
// loop. End-to-end tests spin an actual browser against a running
// application, are slower, and are more prone to environmental flakiness,
// so they are opt-in via --e2e rather than part of the bare command.
// Passing any of the three flags selects exactly the flags given, with no
// implicit default suite added alongside them.
func selectedSuites(goFlag, componentFlag, e2eFlag bool) map[string]bool {
	if !goFlag && !componentFlag && !e2eFlag {
		return map[string]bool{suiteGo: true, suiteComponent: true, suiteE2E: false}
	}
	return map[string]bool{suiteGo: goFlag, suiteComponent: componentFlag, suiteE2E: e2eFlag}
}

// testSummaryResult is `nise test`'s output.Result: every selected suite's
// outcome, in suiteOrder. --json mode marshals it as one structured
// document; nothing about an individual suite's raw child output ever
// reaches that document (see lineForwarder's doc comment).
type testSummaryResult struct {
	Suites []suiteResult `json:"suites"`
}

// Human renders one line per suite.
func (r testSummaryResult) Human() string {
	var b strings.Builder
	for i, s := range r.Suites {
		if i > 0 {
			b.WriteString("\n")
		}
		switch s.Status {
		case string(statusSkipped):
			fmt.Fprintf(&b, "[SKIPPED] %s — %s", s.Name, s.Reason)
		default:
			fmt.Fprintf(&b, "[%s] %s (%s, %.2fs)", strings.ToUpper(s.Status), s.Name, s.Command, s.DurationSeconds)
		}
	}
	return b.String()
}

// testCommand implements `nise test` (Nise task M1-013): a wrapper that
// runs a generated project's Go, browser-component, and end-to-end
// suites. See this file's doc comments on selectedSuites, planGoSuite,
// and planPackageScriptSuite for the default-selection and
// never-invent-a-command rules; see lineForwarder for how child output
// reaches the user without ever entering the JSON stream.
func testCommand() *Command {
	var goFlag, componentFlag, e2eFlag bool
	return &Command{
		Name:  "test",
		Short: "Run the project's Go, browser-component, and end-to-end test suites",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.BoolVar(&goFlag, "go", false, "run the Go suite")
			fs.BoolVar(&componentFlag, "component", false, "run the browser-component suite")
			fs.BoolVar(&e2eFlag, "e2e", false, "run the end-to-end suite")
			return fs
		},
		Run: func(ctx context.Context, env *Env) error {
			wd, err := os.Getwd()
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"could not determine the current directory",
					"Retry from a directory you have permission to read.",
				)
			}

			root, found, err := findProjectRoot(wd)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					fmt.Sprintf("could not search for %s", recipe.FileName),
					"Check filesystem permissions for the current directory and its parents.",
				).WithDocs("docs/commands/test.md")
			}
			if !found {
				return clierr.Precondition(
					fmt.Sprintf("no %s found in the current directory or any parent", recipe.FileName),
					`Run "nise new" to create a project, or run "nise test" from inside one.`,
				).WithCode("test.no_project").WithDocs("docs/commands/test.md")
			}
			if _, err := recipe.Load(os.DirFS(root), recipe.FileName); err != nil {
				return clierr.Wrap(err, clierr.ExitPrecondition,
					fmt.Sprintf("%s does not parse", filepath.Join(root, recipe.FileName)),
					"Fix nise.json by hand, or restore it from version control; see docs/adr/0010-project-recipe-format.md.",
				).WithCode("test.invalid_recipe").WithDocs("docs/commands/test.md")
			}

			selected := selectedSuites(goFlag, componentFlag, e2eFlag)
			plans := map[string]suitePlan{
				suiteGo:        planGoSuite(root),
				suiteComponent: planPackageScriptSuite(suiteComponent, root, componentScriptNames),
				suiteE2E:       planPackageScriptSuite(suiteE2E, root, e2eScriptNames),
			}

			// signal.NotifyContext takes over SIGINT handling for the
			// lifetime of this Run call: a Ctrl-C now cancels sigCtx
			// (which exec.CommandContext-backed runSuite reacts to by
			// killing the running child immediately) instead of the
			// process's default exit. defer stop() restores the default
			// disposition once this command returns, so a *second*
			// Ctrl-C after that behaves normally rather than being
			// silently absorbed forever.
			sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			var results []suiteResult
			var firstFailure *suiteResult
			for _, name := range suiteOrder {
				if !selected[name] {
					continue
				}
				res := runSuite(sigCtx, plans[name], env.Args, env.Out)
				results = append(results, res)
				if res.Status == string(statusFailed) && firstFailure == nil {
					f := res
					firstFailure = &f
				}
			}

			env.Out.Result(testSummaryResult{Suites: results})

			if firstFailure != nil {
				return clierr.New(clierr.ExitError,
					fmt.Sprintf("%s suite failed", firstFailure.Name),
					"See the suite's output above for details, or re-run with --verbose.",
				).WithCode("test.suite_failed").
					WithDetail("suite", firstFailure.Name).
					WithDocs("docs/commands/test.md")
			}
			if sigCtx.Err() != nil {
				return clierr.New(clierr.ExitError,
					"test run interrupted",
					"Re-run \"nise test\" to retry.",
				).WithCode("test.interrupted").WithDocs("docs/commands/test.md")
			}
			return nil
		},
	}
}
