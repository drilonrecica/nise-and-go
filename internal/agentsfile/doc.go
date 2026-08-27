// Package agentsfile generates the two static, machine-readable artifacts
// nise writes for external coding tools to read: AGENTS.md, a prose brief
// on the project's stack, layout, ownership rules, build/test commands, and
// non-negotiable conventions; and .nise/architecture.json, a versioned JSON
// description of the same layout plus the recipe's module selection.
//
// This package is a static artifact generator only, exactly as
// privateDocs/DECISIONS.md's AI section requires: it makes no network
// call, invokes no external tool, and contains no AI or model-provider
// code. Every byte of output is derived from the project's recipe
// (internal/recipe) and this package's own compiled-in knowledge of
// docs/generated-application-layout.md. Generate is a pure function of its
// recipe.Recipe argument: calling it twice with an equal recipe always
// returns byte-identical output, satisfying constraints.md's determinism
// requirement (no timestamp, hostname, username, or absolute path ever
// enters either document).
//
// Both outputs are Nise-owned: they carry an ownership marker and are
// regenerated, never hand-edited. Write refuses to overwrite either file
// when its on-disk content no longer verifies against the content hash
// embedded in it at generation time (see hash.go) — the signal that a
// human (or something else) has edited it since — unless the caller passes
// force. See docs/commands/agents.md for the command-level contract.
//
// This package imports only the standard library and internal/recipe.
package agentsfile
