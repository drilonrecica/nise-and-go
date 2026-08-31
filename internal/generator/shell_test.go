package generator_test

import (
	"strings"
	"testing"
)

// TestGeneratedApplicationShell pins the shell's structure and the
// accessibility properties that are easy to lose in a refactor and invisible
// when they are gone (Nise task M6-005).
func TestGeneratedApplicationShell(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		// Every route lives inside the authenticated group, so the shell is
		// the layout rather than something each page remembers to import.
		"frontend/src/routes/(app)/+layout.svelte": {
			"import AppShell from '$lib/components/shell/AppShell.svelte';",
			"<AppShell name=",
		},
		"frontend/src/lib/components/shell/AppShell.svelte": {
			// The skip link is first in the tab order and visible only on
			// focus. Without it a keyboard user passes every navigation link
			// on every page before reaching the content.
			`href="#main"`,
			"Skip to content",
			`<main id="main" tabindex="-1"`,
		},
		"frontend/src/lib/components/shell/Sidebar.svelte": {
			"<aside",
			// Two <nav> landmarks in one document must be named, or a screen
			// reader offers a list of identical "navigation" entries.
			`<nav aria-label="Main"`,
			"aria-pressed={shell.sidebarCollapsed}",
		},
		"frontend/src/lib/components/shell/NavDrawer.svelte": {
			// showModal() supplies the focus trap, inert background, Escape,
			// and top layer that a hand-written drawer gets wrong.
			"dialog.showModal()",
			`aria-label="Navigation"`,
			`id="navigation-drawer"`,
		},
		"frontend/src/lib/components/shell/TopBar.svelte": {
			"<header",
			`aria-controls="navigation-drawer"`,
			"aria-expanded={shell.drawerOpen}",
		},
		"frontend/src/lib/components/shell/Breadcrumbs.svelte": {
			`aria-label="Breadcrumb"`,
			`aria-current="page"`,
		},
		"frontend/src/lib/components/shell/SidebarNav.svelte": {
			"import { isCurrent, navigation } from '$lib/navigation';",
			`aria-current={current ? 'page' : undefined}`,
			// Collapsed hides the label visually and keeps it in the
			// accessibility tree; display:none would leave an unlabelled link.
			"class:sr-only={collapsed}",
		},
		"frontend/src/lib/components/ui/Menu.svelte": {
			`aria-haspopup="menu"`,
			"aria-expanded={open}",
			`role="menu"`,
			// Returning focus to the trigger on dismissal is the part that is
			// easy to skip and impossible for a keyboard user to work around.
			"triggerElement?.focus()",
			"case 'ArrowDown':",
			"case 'ArrowUp':",
			"case 'Home':",
			"case 'End':",
			"case 'Tab':",
			// Escape has to be handled on the document: opening with the mouse
			// leaves focus on the trigger, which is a sibling of the menu.
			"document.addEventListener('keydown', onKeyDown, true);",
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

	// The sidebar and the drawer draw the same list. Two copies of it would be
	// two places to add a section and one place to forget.
	for _, path := range []string{
		"frontend/src/lib/components/shell/Sidebar.svelte",
		"frontend/src/lib/components/shell/NavDrawer.svelte",
	} {
		if !strings.Contains(content[path], "import SidebarNav from '$lib/components/shell/SidebarNav.svelte';") {
			t.Errorf("%s does not draw the shared navigation list", path)
		}
	}

	// Nothing outside the shell reads the navigation model directly; it is
	// data one component renders, which is what makes `nise generate` able to
	// append to it.
	for path, body := range content {
		if !strings.HasPrefix(path, "frontend/src/") || path == "frontend/src/lib/navigation.ts" {
			continue
		}
		if !strings.Contains(body, "from '$lib/navigation'") {
			continue
		}
		switch path {
		case "frontend/src/lib/components/shell/SidebarNav.svelte",
			"frontend/src/lib/components/shell/Breadcrumbs.svelte",
			"frontend/src/lib/navigation.test.ts":
		default:
			t.Errorf("%s imports the navigation model; only the shell should", path)
		}
	}
}
