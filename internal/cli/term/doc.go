// Package term detects the capabilities of the terminal the nise CLI is
// running in: whether stdout/stderr are interactive terminals, and the
// informal environment-variable conventions (NO_COLOR, CLICOLOR,
// CLICOLOR_FORCE, CI, TERM=dumb) that scripts and users rely on to control
// color and animation.
//
// Detection happens once, at process start, and the result is passed
// explicitly to whatever needs it. Nothing in this package is a global: two
// calls to Detect with different inputs never interfere with each other,
// which is what makes it possible to unit test every combination of
// environment and file-descriptor state without a real terminal.
package term
