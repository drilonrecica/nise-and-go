// Package output is the nise CLI's only writer boundary: every byte a
// command prints goes out through a Writer built by this package, never
// through fmt.Println or a raw io.Writer held by command code directly.
//
// The central guarantee this package exists to provide is structural, not
// conventional: in JSON mode, ANSI escape codes cannot reach stdout, no
// matter what a command passes in. That is enforced two ways. First,
// jsonWriter's Line/Success/Verbosef/Banner/Spinner methods — the
// prose-oriented, human-only vocabulary — are no-ops; only Result and Error
// write anything in JSON mode, and both write exactly one JSON document per
// call. Second, and this is the belt-and-suspenders part a regression test
// checks (see json_test.go's TestJSONWriterNeverEmitsANSI and
// json_source_test.go's source scan), every document jsonWriter writes
// passes through stripANSI right before it reaches stdout, which deletes
// any ESC (0x1b) byte regardless of where it came from. A future command
// that accidentally hands a pre-colored string to Result cannot leak ANSI
// through this writer even if every other safeguard is bypassed.
//
// Color, animation, quiet, and verbose are resolved once by the caller
// (see internal/cli's flag and internal/cli/term's capability handling)
// into an Options value and passed to New explicitly. Nothing in this
// package reads an environment variable or a global.
package output
