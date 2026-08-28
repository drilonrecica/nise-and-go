// Package golden holds the generated-tree conformance suite.
//
// It is the authority on what `nise new` produces: for each recipe variant,
// a committed manifest naming every path, its mode, and the SHA-256 of its
// contents, plus committed full contents for the subset of files whose text
// a reviewer has to actually read in a diff.
//
// # What this suite is for
//
// internal/generator's own tests check that generation works. This suite
// checks that it has not silently changed. The two questions are different:
// a template edit that compiles, renders, and passes every unit test can
// still remove a line from .gitignore, downgrade a pinned frontend version,
// or drop a security header from the generated router, and nothing but a
// committed record of the previous output will say so.
//
// # The rule for what is stored as content, and what only as a hash
//
// A full content snapshot of a whole SvelteKit tree is noise: nobody reviews
// four hundred lines of CSS in a golden diff, and a suite whose diffs are not
// read is a suite that stops working. So contents are committed for two
// categories only:
//
//  1. Every nise-owned file. Nise owns them outright and regenerates them on
//     every upgrade; users are told never to edit them, which makes this
//     suite the only place a change to them is ever reviewed. This category
//     is enforced structurally — TestContentGoldensCoverEveryNiseOwnedFile
//     fails when a new nise-owned template lands without one.
//
//  2. Application-owned files whose *content* encodes a security,
//     dependency, deployment, or configuration-contract decision: go.mod and
//     frontend/package.json (the dependency set), .gitignore and
//     .dockerignore (what stays out of version control and out of the image
//     build context), .env.example (every variable the application reads,
//     including which ones are secrets), deploy/Dockerfile and
//     deploy/compose.yaml (the runtime image and its user, ports, and
//     services), internal/platform/config/config.go (the fail-closed
//     production validation), and internal/platform/httpapi/router.go (the
//     ordered security middleware core).
//
// Everything else — READMEs and other prose, Svelte components, CSS, the
// HTML shell, the favicon, tsconfig/svelte.config/vite.config, the Makefile —
// is pinned by SHA-256 in the manifest alone. A change to one of those still
// fails this suite; it simply shows up as a one-line hash change rather than
// as a wall of text, and the template diff in the same commit is what a
// reviewer reads instead.
//
// # The manifest is sorted by path
//
// Deliberately, and this is the one thing about the format worth stating
// twice. The suite this one replaced kept its manifests in
// internal/generator/testdata and sorted them by hash first, which meant any
// content change reshuffled the whole file and turned every review diff into
// an unreadable permutation — a golden diff nobody can read is a golden
// suite that has stopped working. Sorting by path means a changed file is
// one changed line, a new file is one added line, and a renamed file is one
// of each.
//
// That older suite has been deleted (it was hash-only, hash-sorted, carried
// no modes, and committed no contents), so this is now the only committed
// record of generated output, and one template change needs one regeneration
// command.
//
// # Regenerating
//
//	UPDATE_GOLDEN=1 go test ./test/golden/...
//
// An environment variable rather than a -update flag. When this suite was
// written, internal/generator's older golden test still registered a
// package-level -update flag, and two test packages claiming one flag name
// in a single `go test ./...` invocation is a collision waiting to happen;
// the older suite is gone now, but an environment variable also reads
// unambiguously in CI logs and in a shell history, so it stays. Review the
// resulting diff: it is the record of exactly what a generated project
// contains.
package golden
