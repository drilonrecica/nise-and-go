package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// TestGeneratedFrontendBuildPipeline pins the contract that turns a
// SvelteKit source tree into bytes inside the Go binary (Nise task M6-001).
//
// Every fragment below is load-bearing in a way a reviewer cannot see from
// one file alone: the adapter's output directory has to be the directory the
// Go package embeds, the embed pattern has to be all: or SvelteKit's _app/
// is silently skipped, the build output must never be committed, and the
// single-document contract has to hold or the Content Security Policy hash
// computed from that document at startup covers the wrong bytes.
func TestGeneratedFrontendBuildPipeline(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"frontend/svelte.config.js": {
			"import adapter from '@sveltejs/adapter-static'",
			"pages: '../internal/platform/webui/embedded/client'",
			"assets: '../internal/platform/webui/embedded/client'",
			"fallback: 'index.html'",
			"strict: true",
		},
		"frontend/src/routes/+layout.ts": {
			"export const ssr = false;",
			"export const prerender = false;",
		},
		"internal/platform/webui/webui.go": {
			"//go:embed all:embedded",
			`clientDir = "embedded/client"`,
			`indexName = "index.html"`,
			`placeholderPath = "embedded/placeholder.html"`,
		},
		".gitignore": {
			"/internal/platform/webui/embedded/client/",
		},
		"Makefile": {
			"dist: web-build build",
			"web-build:",
			"\tpnpm --dir frontend build",
			"web-clean:",
		},
		"deploy/Dockerfile": {
			"RUN pnpm --dir frontend build",
		},
	}

	for path, fragments := range wants {
		body, exists := content[path]
		if !exists {
			t.Errorf("generated project lacks %s", path)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}
}

// TestGeneratedFrontendCarriesNoInlineStyleAttribute enforces the rule the
// generated app.html states in prose: the Content Security Policy has no
// style-src-attr 'unsafe-inline', so a literal style attribute anywhere in
// the shipped markup is a blocked declaration and a console violation, not a
// style. Svelte's style: directive and ordinary classes are the alternatives,
// and both are unaffected by this check.
func TestGeneratedFrontendCarriesNoInlineStyleAttribute(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "frontend/src/") {
			continue
		}
		if !strings.HasSuffix(file.Path, ".svelte") && !strings.HasSuffix(file.Path, ".html") {
			continue
		}
		for _, marker := range []string{` style="`, ` style='`, " style={"} {
			if strings.Contains(string(file.Content), marker) {
				t.Errorf("%s carries a literal %q attribute; the Content Security Policy blocks it — use a class or the style: directive", file.Path, strings.TrimSpace(marker))
			}
		}
	}
}
