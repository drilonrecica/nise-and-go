// Package main is the entry point for the nise CLI binary.
//
// main does nothing but call run and os.Exit, so the CLI's behavior is
// testable in-process without a subprocess. The command switch inside run is a
// deliberate stub: M1-002 replaces it with the real dispatch layer in
// internal/cli.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/drilonrecica/nise-and-go/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI with the given arguments (excluding the program name)
// and returns the process exit code. All output goes to the provided writers.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "nise: no command given")
		fmt.Fprintln(stderr, `Run "nise version" to print the version.`)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, version.Get())
		return 0
	default:
		fmt.Fprintf(stderr, "nise: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, `Run "nise version" to print the version.`)
		return 2
	}
}
