// Package main is the entry point for the nise CLI binary.
//
// main does nothing but call run and os.Exit, so the CLI's behavior is
// testable in-process without a subprocess. All command dispatch, flag
// handling, terminal capability detection, and output/error rendering live
// in internal/cli and its subpackages; main's only job is wiring the real
// process (os.Stdin/Stdout/Stderr, os.Getenv) into internal/cli.Execute.
package main

import (
	"io"
	"os"

	"github.com/drilonrecica/nise-and-go/internal/cli"
	"github.com/drilonrecica/nise-and-go/internal/cli/term"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI with the given arguments (excluding the program
// name) and returns the process exit code. All output goes to the provided
// writers. stdin is always the real os.Stdin: no nise command needs a
// fabricated one in a test, since internal/cli.Execute already takes an
// injected IO for its own tests.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Execute(args, cli.IO{
		Stdin:      os.Stdin,
		Stdout:     stdout,
		Stderr:     stderr,
		StdoutStat: statOf(stdout),
		StderrStat: statOf(stderr),
		Getenv:     os.Getenv,
	})
}

// statOf returns w as a term.FileStat when it is one (os.Stdout and
// os.Stderr both are), or nil otherwise (for example a *strings.Builder in
// a test, which term.Detect correctly treats as "not a terminal").
func statOf(w io.Writer) term.FileStat {
	if f, ok := w.(term.FileStat); ok {
		return f
	}
	return nil
}
