package generator_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// legacySvelte are the Svelte 4 constructs no generated file may contain
// (Nise task M6-002). They are the same rules the generated project's own
// frontend/scripts/check-runes.mjs enforces on application code; this test
// enforces them on what Nise itself writes, so the starter can never be the
// source of the mixed-mode codebase that script exists to prevent.
//
// Svelte 5 still accepts every one of them, which is exactly why a test is
// needed: none of these is a compile error or a svelte-check finding.
var legacySvelte = []struct {
	pattern *regexp.Regexp
	what    string
}{
	{regexp.MustCompile(`(?m)^[ \t]*\$:`), "the legacy reactive statement $:"},
	{regexp.MustCompile(`\bexport\s+let\b`), "the legacy prop declaration export let"},
	{regexp.MustCompile(`\$\$(props|restProps|slots)\b`), "legacy $$props/$$restProps/$$slots"},
	{regexp.MustCompile(`\bcreateEventDispatcher\b`), "the legacy createEventDispatcher"},
	{regexp.MustCompile(`\b(beforeUpdate|afterUpdate)\b`), "the legacy beforeUpdate/afterUpdate lifecycle"},
	{regexp.MustCompile(`from\s*['"]svelte/store['"]`), "an import from svelte/store"},
	{regexp.MustCompile(`\son:[a-zA-Z]+(\||[=\s/>])`), "the legacy on: event directive"},
	{regexp.MustCompile(`<slot[\s/>]`), "the legacy <slot> element"},
	{regexp.MustCompile(`\slet:[a-zA-Z]`), "the legacy let: slot property"},
}

// TestGeneratedSvelteIsRunesOnly asserts every generated Svelte source is
// free of Svelte 4 syntax, in both recipe variants.
func TestGeneratedSvelteIsRunesOnly(t *testing.T) {
	t.Parallel()

	for _, options := range []generator.Options{defaultOptions(), allModulesOptions()} {
		files, err := generator.Plan(options)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		for _, file := range files {
			if !isSvelteSource(file.Path) {
				continue
			}
			// The checker itself names every construct it refuses, so it
			// would match all of them.
			if strings.HasSuffix(file.Path, "check-runes.mjs") {
				continue
			}
			for _, rule := range legacySvelte {
				if rule.pattern.Match(file.Content) {
					t.Errorf("%s uses %s; generated Svelte is runes-only", file.Path, rule.what)
				}
			}
		}
	}
}

// TestGeneratedProjectEnforcesRunesOnly asserts the convention is enforced in
// the generated project too, and not only in this repository's tests: the
// checker is written, it is reachable as a script, and `pnpm check` runs it.
// Without the wiring the file would be documentation with an interpreter.
func TestGeneratedProjectEnforcesRunesOnly(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	script, exists := content["frontend/scripts/check-runes.mjs"]
	if !exists {
		t.Fatal("generated project lacks frontend/scripts/check-runes.mjs")
	}
	for _, fragment := range []string{
		"const scriptRules = [",
		"const markupRules = [",
		"process.exit(1);",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("check-runes.mjs lacks %q", fragment)
		}
	}

	pkg := content["frontend/package.json"]
	for _, fragment := range []string{
		`"check:runes": "node scripts/check-runes.mjs src"`,
		`"check": "pnpm run api:check && pnpm run messages:compile && pnpm run check:runes &&`,
	} {
		if !strings.Contains(pkg, fragment) {
			t.Errorf("frontend/package.json lacks %q", fragment)
		}
	}
}

// isSvelteSource reports whether path is a Svelte component or a Svelte
// reactive module (.svelte.ts / .svelte.js).
func isSvelteSource(path string) bool {
	return strings.HasSuffix(path, ".svelte") ||
		strings.HasSuffix(path, ".svelte.ts") ||
		strings.HasSuffix(path, ".svelte.js")
}
