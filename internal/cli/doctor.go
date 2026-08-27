package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/doctor"
	"github.com/drilonrecica/nise-and-go/internal/version"
)

// checkResult adapts a single doctor.Check to output.Result. doctor.Check's
// own JSON tags are reused as-is (an anonymous embed marshals identically
// to the embedded type — no wrapper field appears in the encoded object),
// so `nise doctor --json`'s per-check documents and doctor.Check's Go
// representation never drift into two subtly different shapes.
//
// doctorCommand emits one of these per check rather than one aggregate
// object holding every check, deliberately: docs/cli-output.md's JSON mode
// section names doctor by name as the example of a command that "reports
// several discrete events" and "can emit one line per event without
// changing the [one-JSON-document-per-line] contract" — each check is one
// such event. This also sidesteps a real conflict a single aggregate
// Result would create: a failing check has to end the command with a
// non-zero exit via a returned *clierr.Error (Execute's only mechanism for
// setting the exit code — see dispatch.go's runLeaf), and emitting both an
// aggregate Result and a final Error would put two competing documents
// with overlapping information on the same stdout stream. Emitting one
// document per check plus, only on failure, one trailing error document
// keeps every line meaning exactly one thing.
type checkResult struct {
	doctor.Check
}

// Human renders one check as a short, greppable block: a bracketed status
// tag, the check name, and — for anything that was actually recorded —
// what was found, what is required, and, when not ok, the exact recovery
// action. This mirrors the brief's "honest status" requirement in the
// human renderer, not just the JSON one: a reader scanning terminal output
// sees the same found/required/remedy facts a script parsing --json does.
func (c checkResult) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", strings.ToUpper(string(c.Status)), c.Name)
	if c.Found != "" {
		fmt.Fprintf(&b, "\n    found:    %s", c.Found)
	}
	if c.Required != "" {
		fmt.Fprintf(&b, "\n    required: %s", c.Required)
	}
	if c.Remedy != "" {
		fmt.Fprintf(&b, "\n    remedy:   %s", c.Remedy)
	}
	return b.String()
}

// doctorCommand implements `nise doctor` (Nise task M1-006): it verifies
// the tools the golden profile needs are present and new enough, and, when
// run inside a generated project, that the project's recipe parses and is
// compatible with the running nise build. See internal/doctor for the
// checks themselves; this file only adapts internal/doctor's Report to the
// CLI's output and exit-code contract.
func doctorCommand() *Command {
	return &Command{
		Name:  "doctor",
		Short: "Check that required tools and the current project are ready",
		Run: func(ctx context.Context, env *Env) error {
			wd, err := os.Getwd()
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"could not determine the current directory",
					"Retry from a directory you have permission to read.",
				)
			}

			report := doctor.Run(ctx, doctor.Options{
				WorkDir:     wd,
				NiseVersion: version.Get().Version,
			})

			var okCount, warnCount, failCount, skippedCount int
			for _, c := range report.Checks {
				env.Out.Result(checkResult{c})
				switch c.Status {
				case doctor.StatusOK:
					okCount++
				case doctor.StatusWarn:
					warnCount++
				case doctor.StatusFail:
					failCount++
				case doctor.StatusSkipped:
					skippedCount++
				}
			}
			env.Out.Line(fmt.Sprintf("%d ok, %d warn, %d fail, %d skipped", okCount, warnCount, failCount, skippedCount))

			if report.Failed() {
				return clierr.Precondition(
					"one or more required checks failed",
					`Fix the failing checks above, then re-run "nise doctor".`,
				).WithCode("doctor.precondition_failed").WithDocs("docs/commands/doctor.md")
			}
			return nil
		},
	}
}
