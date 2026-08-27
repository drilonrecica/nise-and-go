package doctor

import "testing"

// TestParseVersionRealOutputShapes locks the parser to the exact strings
// observed by running each tool on the reference development machine
// (docs/toolchain.md's verified environment), not to assumed shapes. Every
// input below is a verbatim capture:
//
//	$ go version
//	go version go1.26.5-X:nodwarf5 linux/amd64
//	$ node --version
//	v22.22.2
//	$ pnpm --version
//	10.33.0
//	$ docker --version
//	Docker version 29.6.2, build dfc4efb
//	$ podman --version
//	podman version 5.8.4
//	$ go tool sqlc version
//	v1.31.1
//	$ go tool goose --version
//	goose version: v3.27.3
//	$ go tool oapi-codegen -version
//	github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
//	v2.8.0
func TestParseVersionRealOutputShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Version
	}{
		{
			name:  "go version, with this machine's vendor suffix after the patch number",
			input: "go version go1.26.5-X:nodwarf5 linux/amd64\n",
			want:  Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name:  "node --version",
			input: "v22.22.2\n",
			want:  Version{Major: 22, Minor: 22, Patch: 2, Raw: "22.22.2"},
		},
		{
			name:  "pnpm --version",
			input: "10.33.0\n",
			want:  Version{Major: 10, Minor: 33, Patch: 0, Raw: "10.33.0"},
		},
		{
			name:  "docker --version",
			input: "Docker version 29.6.2, build dfc4efb\n",
			want:  Version{Major: 29, Minor: 6, Patch: 2, Raw: "29.6.2"},
		},
		{
			name:  "podman --version",
			input: "podman version 5.8.4\n",
			want:  Version{Major: 5, Minor: 8, Patch: 4, Raw: "5.8.4"},
		},
		{
			name:  "go tool sqlc version",
			input: "v1.31.1\n",
			want:  Version{Major: 1, Minor: 31, Patch: 1, Raw: "1.31.1"},
		},
		{
			name:  "go tool goose --version, with its own \"goose version:\" label",
			input: "goose version: v3.27.3\n",
			want:  Version{Major: 3, Minor: 27, Patch: 3, Raw: "3.27.3"},
		},
		{
			name:  "go tool oapi-codegen -version, two lines, import path first",
			input: "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen\nv2.8.0\n",
			want:  Version{Major: 2, Minor: 8, Patch: 0, Raw: "2.8.0"},
		},
		{
			// Go's own release-candidate spelling: NO separator before
			// "rc", unlike the hyphenated semver convention. A pattern
			// written only for "-rc1" silently drops this marker and
			// reports a plain 1.26.0 — see
			// TestParseVersionGoReleaseCandidateFailsAtLeastFinalRelease
			// below for why that used to be a false ok.
			name:  "go version, an actual go1.26 release-candidate build",
			input: "go version go1.26rc1 linux/amd64\n",
			want:  Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26rc1", Prerelease: "rc1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tt.input)
			if err != nil {
				t.Fatalf("ParseVersion(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseVersionTolerance covers the specific shapes the brief calls out
// by name — go1.26.5, v1.2.3, 1.2.3-rc1, and a vendor suffix — independent
// of any particular tool's real output.
func TestParseVersionTolerance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Version
	}{
		{"go-prefixed, no separator", "go1.26.5", Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"}},
		{"v-prefixed", "v1.2.3", Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"}},
		{
			"hyphenated prerelease suffix is recognized and kept",
			"1.2.3-rc1",
			Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3-rc1", Prerelease: "rc1"},
		},
		{
			"unseparated prerelease suffix (go's own convention) is recognized and kept",
			"1.2.3rc1",
			Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3rc1", Prerelease: "rc1"},
		},
		{
			"other recognized prerelease keywords",
			"2.0.0-beta2",
			Version{Major: 2, Minor: 0, Patch: 0, Raw: "2.0.0-beta2", Prerelease: "beta2"},
		},
		{
			"prerelease keyword matching is case-insensitive",
			"1.2.3-RC1",
			Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3-RC1", Prerelease: "rc1"},
		},
		{
			"build-metadata suffix is not mistaken for a prerelease",
			"1.2.3+build.5",
			Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"},
		},
		{
			"vendor suffix glued to the version with no word boundary is not mistaken for a prerelease",
			"1.2.3-X:nodwarf5",
			Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"},
		},
		{"missing patch component defaults to zero", "1.2", Version{Major: 1, Minor: 2, Patch: 0, Raw: "1.2"}},
		{"leading and trailing whitespace", "  1.2.3  \n", Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"}},
		{"embedded in unrelated prose", "some-tool, revision 1.2.3 (stable)", Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tt.input)
			if err != nil {
				t.Fatalf("ParseVersion(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseVersionGoReleaseCandidateFailsAtLeastFinalRelease is the
// regression test for the reviewer's finding on fix round 1: a Go release
// candidate (go1.26rc1, Go's actual unseparated spelling — not the
// hyphenated "1.26-rc1" a semver-shaped pattern would expect) must NOT
// satisfy AtLeast against the final 1.26.0 release. Before this fix,
// versionPattern's match stopped at "1.26" (no digit or dot follows
// "1.26" in "1.26rc1"), silently dropping the "rc1" marker entirely and
// reporting a plain, "final" 1.26.0 — which then passed AtLeast(1.26.0),
// telling a developer running an unreleased compiler that their toolchain
// was fine.
func TestParseVersionGoReleaseCandidateFailsAtLeastFinalRelease(t *testing.T) {
	t.Parallel()

	rc, err := ParseVersion("go version go1.26rc1 linux/amd64")
	if err != nil {
		t.Fatalf("ParseVersion returned error: %v", err)
	}
	final, err := ParseVersion("1.26.0")
	if err != nil {
		t.Fatalf("ParseVersion(final) returned error: %v", err)
	}

	if rc.AtLeast(final) {
		t.Errorf("go1.26rc1.AtLeast(1.26.0) = true, want false: a release candidate is not the release it names")
	}
	if got := rc.Compare(final); got != -1 {
		t.Errorf("go1.26rc1.Compare(1.26.0) = %d, want -1", got)
	}

	// The hyphenated semver form must behave identically: this is a
	// pre-release marker recognized regardless of the separator style,
	// not a Go-specific special case.
	hyphenated, err := ParseVersion("1.26.0-rc1")
	if err != nil {
		t.Fatalf("ParseVersion(hyphenated) returned error: %v", err)
	}
	if hyphenated.AtLeast(final) {
		t.Errorf("1.26.0-rc1.AtLeast(1.26.0) = true, want false")
	}
}

// TestParseVersionHostileInput covers inputs a real tool should never
// print but a doctor check must still survive without panicking: empty
// output, output with no version-shaped substring, and a numeric
// component too large for an int.
func TestParseVersionHostileInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"only whitespace", "   \n\t"},
		{"no digits at all", "command not found"},
		{"a single bare number, no dot", "42"},
		{"a version-shaped major component that overflows int", "99999999999999999999.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseVersion(tt.input)
			if err == nil {
				t.Fatalf("ParseVersion(%q) = nil error, want an error", tt.input)
			}
		})
	}
}

func TestVersionCompareAndAtLeast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		a, b      Version
		compare   int
		aAtLeastB bool
	}{
		{
			name: "equal", compare: 0, aAtLeastB: true,
			a: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
			b: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name: "raw text ignored in comparison", compare: 0, aAtLeastB: true,
			a: Version{Major: 1, Minor: 26, Patch: 5, Raw: "go1.26.5-vendor"},
			b: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name: "greater major", compare: 1, aAtLeastB: true,
			a: Version{Major: 2, Minor: 0, Patch: 0, Raw: "2.0.0"},
			b: Version{Major: 1, Minor: 99, Patch: 99, Raw: "1.99.99"},
		},
		{
			name: "lesser major", compare: -1, aAtLeastB: false,
			a: Version{Major: 1, Minor: 0, Patch: 0, Raw: "1.0.0"},
			b: Version{Major: 2, Minor: 0, Patch: 0, Raw: "2.0.0"},
		},
		{
			name: "greater minor, equal major", compare: 1, aAtLeastB: true,
			a: Version{Major: 1, Minor: 27, Patch: 0, Raw: "1.27.0"},
			b: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name: "lesser minor, equal major", compare: -1, aAtLeastB: false,
			a: Version{Major: 1, Minor: 25, Patch: 9, Raw: "1.25.9"},
			b: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26.0"},
		},
		{
			name: "greater patch only", compare: 1, aAtLeastB: true,
			a: Version{Major: 1, Minor: 26, Patch: 6, Raw: "1.26.6"},
			b: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name: "lesser patch only", compare: -1, aAtLeastB: false,
			a: Version{Major: 1, Minor: 26, Patch: 4, Raw: "1.26.4"},
			b: Version{Major: 1, Minor: 26, Patch: 5, Raw: "1.26.5"},
		},
		{
			name: "same major.minor.patch, prerelease sorts below the release", compare: -1, aAtLeastB: false,
			a: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26rc1", Prerelease: "rc1"},
			b: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26.0"},
		},
		{
			name: "the release satisfies AtLeast against its own release candidate", compare: 1, aAtLeastB: true,
			a: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26.0"},
			b: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26rc1", Prerelease: "rc1"},
		},
		{
			name: "two different prerelease tags at the same core compare lexicographically", compare: -1, aAtLeastB: false,
			a: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26.0-alpha1", Prerelease: "alpha1"},
			b: Version{Major: 1, Minor: 26, Patch: 0, Raw: "1.26.0-beta1", Prerelease: "beta1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.a.Compare(tt.b); got != tt.compare {
				t.Errorf("%+v.Compare(%+v) = %d, want %d", tt.a, tt.b, got, tt.compare)
			}
			if got := tt.a.AtLeast(tt.b); got != tt.aAtLeastB {
				t.Errorf("%+v.AtLeast(%+v) = %v, want %v", tt.a, tt.b, got, tt.aAtLeastB)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	t.Parallel()

	t.Run("plain release", func(t *testing.T) {
		t.Parallel()
		v, err := ParseVersion("go version go1.26.5-X:nodwarf5 linux/amd64")
		if err != nil {
			t.Fatalf("ParseVersion returned error: %v", err)
		}
		if got, want := v.String(), "1.26.5"; got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	})

	t.Run("release candidate keeps its marker visible", func(t *testing.T) {
		t.Parallel()
		v, err := ParseVersion("go version go1.26rc1 linux/amd64")
		if err != nil {
			t.Fatalf("ParseVersion returned error: %v", err)
		}
		if got, want := v.String(), "1.26rc1"; got != want {
			t.Errorf("String() = %q, want %q (a developer reading \"found\" text must see this is an RC)", got, want)
		}
	})
}
