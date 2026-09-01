// Package freeze holds the checks that the V0.1 surface has not grown.
//
// The blueprint's containment mechanism for a framework-first project is a
// hard feature freeze once the V0.1 checklist passes: no new profile, no new
// optional module, no new dependency, unless it replaces an existing item or a
// documented capability cannot work safely without it.
//
// A freeze that lives in a document is a freeze somebody breaks by accident on
// a Tuesday. These tests make the three enumerable parts of it fail a build
// instead: the profile set, the module set, and every dependency a generated
// project pins. None of them can grow without editing a second place that
// says what was intended and, for a dependency, why it is there at all.
//
// What they do not do is stop the framework from growing in ways nobody can
// enumerate — a runtime package, a generated file, an option on an existing
// command. That is a judgement the maintainer makes; this is the part a
// machine can hold.
package freeze
