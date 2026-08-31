package generator_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// curatedComponents is the set nise new writes into
// frontend/src/lib/components/ui/ (Nise task M6-006). It is a fixed list
// rather than a directory walk, because "the set is small and deliberate" is
// the property under test: a component added without a decision fails here.
var curatedComponents = []string{
	"Alert.svelte",
	"Badge.svelte",
	"Button.svelte",
	"Checkbox.svelte",
	"DataTable.svelte",
	"ConfirmDialog.svelte",
	"Dialog.svelte",
	"Field.svelte",
	"Icon.svelte",
	"Input.svelte",
	"Menu.svelte",
	"Pagination.svelte",
	"ProblemAlert.svelte",
	"Select.svelte",
	"Skeleton.svelte",
	"Spinner.svelte",
	"Table.svelte",
	"Textarea.svelte",
	"Toaster.svelte",
	"Tooltip.svelte",
	"icons.ts",
	"toast.svelte.ts",
	"toast.test.ts",
}

func TestGeneratedComponentSet(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	const dir = "frontend/src/lib/components/ui/"
	for _, name := range curatedComponents {
		if _, exists := content[dir+name]; !exists {
			t.Errorf("generated project lacks %s%s", dir, name)
		}
	}
	for path := range content {
		if !strings.HasPrefix(path, dir) {
			continue
		}
		name := strings.TrimPrefix(path, dir)
		if !containsString(curatedComponents, name) {
			t.Errorf("%s is in the component directory but not in the curated set; add it here deliberately or move it", path)
		}
	}
}

// bundledFrontendPackages is what the generated frontend is allowed to ship in
// its bundle, as opposed to what only builds or tests it. It is a closed list
// because every entry is a decision with an entry in docs/dependencies.md, and
// because "what is actually in the bundle" is the number that gets away from a
// project one convenience at a time.
var bundledFrontendPackages = []string{"@tanstack/table-core", "valibot"}

// TestGeneratedFrontendShipsOnlyJustifiedPackages pins ADR 0025 and the
// bundled-dependency list: no headless component library, and nothing in
// `dependencies` that is not in the list above.
func TestGeneratedFrontendShipsOnlyJustifiedPackages(t *testing.T) {
	t.Parallel()

	raw := planContent(t, defaultOptions())["frontend/package.json"]
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		t.Fatalf("frontend/package.json is not valid JSON: %v", err)
	}
	for _, field := range []string{"peerDependencies", "optionalDependencies"} {
		if _, present := pkg[field]; present {
			t.Errorf("frontend/package.json declares %q; a generated application needs neither", field)
		}
	}

	var bundled map[string]string
	if err := json.Unmarshal(pkg["dependencies"], &bundled); err != nil {
		t.Fatalf("dependencies is not an object: %v", err)
	}
	for name := range bundled {
		if !containsString(bundledFrontendPackages, name) {
			t.Errorf("frontend/package.json ships %q in the bundle; add it deliberately here and to docs/dependencies.md", name)
		}
	}
	for _, expected := range bundledFrontendPackages {
		if _, present := bundled[expected]; !present {
			t.Errorf("frontend/package.json no longer ships %q", expected)
		}
	}

	var devDependencies map[string]string
	if err := json.Unmarshal(pkg["devDependencies"], &devDependencies); err != nil {
		t.Fatalf("devDependencies is not an object: %v", err)
	}
	for _, rejected := range []string{"bits-ui", "shadcn-svelte", "@floating-ui/dom", "clsx", "tailwind-merge"} {
		for _, block := range []map[string]string{bundled, devDependencies} {
			if _, present := block[rejected]; present {
				t.Errorf("frontend/package.json pins %q; see docs/adr/0025-owned-ui-primitives.md", rejected)
			}
		}
	}
}

// TestGeneratedComponentAccessibilityContracts pins, per component, the one
// property that is invisible once it is gone. Each of these was chosen
// because losing it produces no error, no warning, and no visible change —
// only a component that some people can no longer use.
func TestGeneratedComponentAccessibilityContracts(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		// A link that acts and a button that navigates are the two most common
		// defects here; separate prop types are what refuse the mix.
		"Button.svelte": {"{#if href !== undefined}", "aria-busy={loading || undefined}"},
		// The label, the help text, the error, and the control, wired by id.
		"Field.svelte":  {"aria-live=\"polite\"", "describedBy", "invalid: Boolean(error)"},
		"Input.svelte":  {"aria-invalid={invalid || undefined}", "aria-describedby={describedBy}"},
		"Select.svelte": {"aria-invalid={invalid || undefined}", "<select"},
		// showModal(), not a hand-rolled focus trap.
		"Dialog.svelte": {"dialog.showModal()", "aria-labelledby={titleId}"},
		// A tooltip describes; it never names.
		"Tooltip.svelte": {`role="tooltip"`, "describedBy", "event.key === 'Escape'"},
		// One live region, present before the first message exists.
		"Toaster.svelte": {`role="status"`, `aria-live="polite"`},
		// Decoration for a region that already says it is busy.
		"Skeleton.svelte": {`aria-hidden="true"`},
		// Screen readers list tables by caption, and a scroll region must be
		// reachable by keyboard.
		"Table.svelte": {"<caption", `tabindex="0"`, `role="region"`},
		// Paging is navigation: real links, real hrefs, and a disabled control
		// that stays put rather than vanishing.
		"Pagination.svelte": {"<nav aria-label={label}", `rel="prev"`, `rel="next"`, `aria-disabled="true"`},
		// The platform's own checkbox, so forced-colors mode still draws it.
		"Checkbox.svelte": {`type="checkbox"`, "accent-primary"},
	}
	for name, fragments := range wants {
		body, exists := content["frontend/src/lib/components/ui/"+name]
		if !exists {
			t.Errorf("generated project lacks %s", name)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Errorf("%s lacks %q", name, fragment)
			}
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
