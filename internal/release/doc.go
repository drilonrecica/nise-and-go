// Package release answers two questions about the running nise binary that
// nothing else can: how it was installed, and whether a newer release exists.
//
// It is the only package in this repository that opens a network connection on
// a user's behalf, and it does so for exactly one command — `nise version
// check`. That is a deliberate, documented exception to the guarantee in
// docs/no-telemetry.md, and the reason this is a separate package rather than
// a file inside internal/cli: Go's import graph is per package, so putting the
// HTTP call here keeps test/nonetwork's allowlist entry pointed at eleven
// lines of purpose-named code instead of at every command nise has.
//
// What it will not do:
//
//   - Run on its own. Nothing calls Latest except a command a person typed.
//   - Identify the caller. The request carries a constant User-Agent with no
//     version in it, no authentication, no cookies, and no other header — so
//     one nise user's request is indistinguishable from another's, and GitHub
//     learns nothing from it that it does not already learn from the TCP
//     connection.
//   - Install anything. Detect exists so the command can print the right
//     instruction for a person to run; nise never modifies a binary, least of
//     all one a package manager owns.
//   - Follow a redirect. The endpoint does not redirect, and a redirect is how
//     a captive portal or an intercepting proxy steers a request somewhere
//     else. Refusing them means this package talks to one host or fails.
package release
