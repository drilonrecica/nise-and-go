// Package templates carries the golden-profile source templates that
// internal/generator renders into a new application.
//
// It exists as a Go package for one reason: go:embed cannot reach outside
// the directory of the package that declares it, and docs/repository-layout.md
// puts the templates at the repository root rather than under
// internal/generator. This file is the whole package — it declares no
// behavior, and nothing here is part of Nise's public surface.
//
// The all: prefix on the embed pattern is deliberate. Without it go:embed
// silently skips every path whose name begins with "_" or "." — the same
// trap that drops SvelteKit's _app/ directory out of a generated
// application's binary. No template file is named that way today, and the
// prefix is what keeps that true by construction rather than by review.
package templates

import "embed"

// FS holds every template file under new/, the tree nise new renders. Paths
// inside it are slash-separated and carry a .tmpl suffix; internal/generator
// maps each one to its output path explicitly.
//
//go:embed all:new
var FS embed.FS

// FeatureFS holds the templates `nise generate feature` and `nise generate
// resource` render into an existing project. They are a separate tree from
// new/ because they are rendered against different data, into a project that
// already exists, and under a different ownership rule: everything they
// produce is application-owned from the moment it is written and is never
// regenerated (ADR 0026).
//
//go:embed all:feature
var FeatureFS embed.FS
