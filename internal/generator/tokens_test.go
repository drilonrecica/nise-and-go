package generator_test

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This file proves the generated design tokens (Nise task M6-003) rather
// than merely asserting they exist.
//
// The generated project ships frontend/scripts/check-contrast.mjs, which is
// what guards an application's own rebrand. It does not guard Nise's default
// palette in this repository's CI, because that gate runs Go and no Node.
// These tests are that gate: the same WCAG 2.2 arithmetic applied to the same
// pairs, over the bytes nise new would actually write.

// tokenBlock captures the custom properties declared by one theme block.
var (
	lightBlockRE = regexp.MustCompile(`(?s):root,\s*\[data-theme='light'\] \{(.*?)\n\}`)
	darkBlockRE  = regexp.MustCompile(`(?s)\[data-theme='dark'\] \{(.*?)\n\}`)
	declRE       = regexp.MustCompile(`(?m)^\s*--([a-z0-9-]+):\s*([^;]+);`)
)

// contrastPairs mirrors the generated checker's table: foreground, background,
// and the minimum ratio WCAG 2.2 requires of that pair. 4.5 is SC 1.4.3 for
// normal text; 3.0 is SC 1.4.11 for the boundary of a user-interface
// component.
var contrastPairs = []struct {
	foreground string
	background string
	minimum    float64
}{
	{"text", "canvas", 4.5},
	{"text", "surface", 4.5},
	{"text", "surface-muted", 4.5},
	{"text-muted", "canvas", 4.5},
	{"text-muted", "surface", 4.5},
	{"text-subtle", "surface", 4.5},
	{"text-subtle", "surface-muted", 4.5},

	{"primary-text", "primary", 4.5},
	{"primary-text", "primary-hover", 4.5},
	{"primary-text", "primary-active", 4.5},
	{"primary-on-soft", "primary-soft", 4.5},
	{"primary", "canvas", 3.0},
	{"primary", "surface", 3.0},

	{"success-text", "success-soft", 4.5},
	{"warning-text", "warning-soft", 4.5},
	{"danger-text", "danger-soft", 4.5},
	{"info-text", "info-soft", 4.5},

	{"text-inverted", "success", 4.5},
	{"text-inverted", "warning", 4.5},
	{"text-inverted", "danger", 4.5},
	{"text-inverted", "info", 4.5},

	{"control-line", "canvas", 3.0},
	{"control-line", "surface", 3.0},
	{"control-line", "surface-muted", 3.0},

	{"ring", "canvas", 3.0},
	{"ring", "surface", 3.0},
	{"ring", "surface-muted", 3.0},
}

// TestGeneratedThemeTokensMeetWCAG holds the default palette to the standard
// the documentation claims for it, in both themes.
func TestGeneratedThemeTokensMeetWCAG(t *testing.T) {
	t.Parallel()

	css := planContent(t, defaultOptions())["frontend/src/app.css"]
	if css == "" {
		t.Fatal("generated project lacks frontend/src/app.css")
	}

	for _, theme := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"light", lightBlockRE},
		{"dark", darkBlockRE},
	} {
		block := theme.re.FindStringSubmatch(css)
		if block == nil {
			t.Fatalf("app.css has no %s theme block", theme.name)
		}
		declared := map[string]string{}
		for _, decl := range declRE.FindAllStringSubmatch(block[1], -1) {
			declared[decl[1]] = strings.TrimSpace(decl[2])
		}

		for _, pair := range contrastPairs {
			front, frontOK := declared[pair.foreground]
			back, backOK := declared[pair.background]
			if !frontOK || !backOK {
				missing := pair.foreground
				if frontOK {
					missing = pair.background
				}
				t.Errorf("%s theme does not declare --%s", theme.name, missing)
				continue
			}
			got, err := contrastRatio(front, back)
			if err != nil {
				t.Errorf("%s theme: --%s on --%s: %v", theme.name, pair.foreground, pair.background, err)
				continue
			}
			// The same 0.005 tolerance the generated checker uses, so a
			// ratio that rounds to the threshold is not a failure.
			if got+0.005 < pair.minimum {
				t.Errorf("%s theme: --%s (%s) on --%s (%s) is %.2f:1, WCAG 2.2 needs %.1f:1",
					theme.name, pair.foreground, front, pair.background, back, got, pair.minimum)
			}
		}
	}
}

// TestGeneratedThemeContract pins the parts of the token system that make it
// a system rather than a stylesheet: the theme is always a resolved attribute,
// Tailwind sees the tokens by reference rather than by value, and the
// contrast checker is wired into the project's own check.
func TestGeneratedThemeContract(t *testing.T) {
	t.Parallel()

	content := planContent(t, defaultOptions())

	wants := map[string][]string{
		"frontend/src/app.html": {
			`<html lang="en" data-theme="light">`,
		},
		"frontend/src/app.css": {
			"@import 'tailwindcss';",
			// Without `inline` Tailwind copies the current value into its
			// own variable at build time and both themes render as light.
			"@theme inline {",
			"--color-canvas: var(--canvas);",
			"color-scheme: light;",
			"color-scheme: dark;",
			":focus-visible {",
			"#svelte-announcer {",
		},
		"frontend/package.json": {
			`"check:contrast": "node scripts/check-contrast.mjs src/app.css"`,
			"pnpm run check:contrast &&",
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

	// A component must reach a color through a token. A raw Tailwind palette
	// utility in generated markup is a color that a rebrand cannot reach.
	rawPalette := regexp.MustCompile(`\b(bg|text|border|ring|fill|stroke|from|to|via)-(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]{2,3}\b`)
	for path, body := range content {
		if !strings.HasPrefix(path, "frontend/src/") || !strings.HasSuffix(path, ".svelte") {
			continue
		}
		if match := rawPalette.FindString(body); match != "" {
			t.Errorf("%s uses the raw palette utility %q; use a design token so a rebrand reaches it", path, match)
		}
	}
}

// contrastRatio computes the WCAG 2.2 contrast ratio between two opaque CSS
// colors written as #rgb or #rrggbb.
func contrastRatio(foreground, background string) (float64, error) {
	front, err := relativeLuminance(foreground)
	if err != nil {
		return 0, err
	}
	back, err := relativeLuminance(background)
	if err != nil {
		return 0, err
	}
	high, low := math.Max(front, back), math.Min(front, back)
	return (high + 0.05) / (low + 0.05), nil
}

func relativeLuminance(color string) (float64, error) {
	digits, found := strings.CutPrefix(strings.TrimSpace(color), "#")
	if !found {
		return 0, fmt.Errorf("%q is not a hex color; the contrast pairs must be opaque", color)
	}
	if len(digits) == 3 {
		expanded := make([]byte, 0, 6)
		for i := 0; i < 3; i++ {
			expanded = append(expanded, digits[i], digits[i])
		}
		digits = string(expanded)
	}
	if len(digits) != 6 {
		return 0, fmt.Errorf("%q is not a 3- or 6-digit hex color", color)
	}
	channels := make([]float64, 3)
	for i := range channels {
		value, err := strconv.ParseUint(digits[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("%q is not a hex color: %w", color, err)
		}
		c := float64(value) / 255
		if c <= 0.04045 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2], nil
}
