package generator_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestGeneratedThemeResolutionIsConsistent guards the one duplication the
// theme mechanism has (Nise task M6-004).
//
// app.html carries a small inline script that resolves the theme before first
// paint, because a module cannot run before the browser paints the page
// background and a dark-mode reader would otherwise get a white flash on
// every cold load. That script necessarily repeats two facts from
// src/lib/theme.svelte.ts: the storage key and the three preference names.
// Repeating them is fine; disagreeing about them is a reader whose choice is
// silently discarded on the next load, with nothing failing anywhere.
func TestGeneratedThemeResolutionIsConsistent(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())
	html := content["frontend/src/app.html"]
	module := content["frontend/src/lib/theme.svelte.ts"]
	if html == "" || module == "" {
		t.Fatal("generated project lacks app.html or src/lib/theme.svelte.ts")
	}

	keyRE := regexp.MustCompile(`themeStorageKey = '([^']+)'`)
	key := keyRE.FindStringSubmatch(module)
	if key == nil {
		t.Fatal("theme.svelte.ts does not declare themeStorageKey")
	}
	if !strings.Contains(html, "localStorage.getItem('"+key[1]+"')") {
		t.Errorf("app.html's first-paint script does not read localStorage key %q", key[1])
	}

	preferencesRE := regexp.MustCompile(`themePreferences = \[([^\]]+)\]`)
	declared := preferencesRE.FindStringSubmatch(module)
	if declared == nil {
		t.Fatal("theme.svelte.ts does not declare themePreferences")
	}
	for _, quoted := range strings.Split(declared[1], ",") {
		name := strings.Trim(strings.TrimSpace(quoted), "'")
		if name == "" {
			continue
		}
		if !strings.Contains(html, "stored === '"+name+"'") {
			t.Errorf("app.html's first-paint script does not accept the stored preference %q", name)
		}
	}

	// The script must write the resolved value, never the preference: writing
	// "system" into data-theme would match neither palette.
	if !strings.Contains(html, `document.documentElement.setAttribute('data-theme', resolved)`) {
		t.Error("app.html's first-paint script does not write the resolved theme to data-theme")
	}
}

// TestGeneratedThemeIsLiveAndMotionAware pins the behavior that separates a
// working theme from a stored string: `system` follows the operating system
// while the page is open, and every animation collapses for a reader who
// asked for reduced motion.
func TestGeneratedThemeIsLiveAndMotionAware(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"frontend/src/lib/theme.svelte.ts": {
			"'(prefers-color-scheme: dark)'",
			"'(prefers-reduced-motion: reduce)'",
			"addEventListener('change', onDark)",
			"removeEventListener('change', onDark)",
			// The preference is stored; the resolution never is.
			"writePreference(globalThis.localStorage, this.#preference)",
		},
		"frontend/src/routes/+layout.svelte": {
			"$effect(() => preferences.start());",
		},
		"frontend/src/app.css": {
			"@media (prefers-reduced-motion: reduce) {",
			// A single frame rather than `none`, so a transitionend
			// listener still fires and nothing waits forever.
			"animation-duration: 0.01ms !important;",
			"transition-duration: 0.01ms !important;",
			"scroll-behavior: auto !important;",
		},
		"frontend/vitest.config.ts": {
			"plugins: [svelte()]",
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
