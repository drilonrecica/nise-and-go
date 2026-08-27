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
		{"go-prefixed, no separator", "go1.26.5", Version{1, 26, 5, "1.26.5"}},
		{"v-prefixed", "v1.2.3", Version{1, 2, 3, "1.2.3"}},
		{"prerelease suffix", "1.2.3-rc1", Version{1, 2, 3, "1.2.3"}},
		{"build-metadata suffix", "1.2.3+build.5", Version{1, 2, 3, "1.2.3"}},
		{"vendor suffix glued to the version with no word boundary", "1.2.3-X:nodwarf5", Version{1, 2, 3, "1.2.3"}},
		{"missing patch component defaults to zero", "1.2", Version{1, 2, 0, "1.2"}},
		{"leading and trailing whitespace", "  1.2.3  \n", Version{1, 2, 3, "1.2.3"}},
		{"embedded in unrelated prose", "some-tool, revision 1.2.3 (stable)", Version{1, 2, 3, "1.2.3"}},
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
		{"equal", Version{1, 26, 5, "1.26.5"}, Version{1, 26, 5, "1.26.5"}, 0, true},
		{"raw text ignored in comparison", Version{1, 26, 5, "go1.26.5-vendor"}, Version{1, 26, 5, "1.26.5"}, 0, true},
		{"greater major", Version{2, 0, 0, "2.0.0"}, Version{1, 99, 99, "1.99.99"}, 1, true},
		{"lesser major", Version{1, 0, 0, "1.0.0"}, Version{2, 0, 0, "2.0.0"}, -1, false},
		{"greater minor, equal major", Version{1, 27, 0, "1.27.0"}, Version{1, 26, 5, "1.26.5"}, 1, true},
		{"lesser minor, equal major", Version{1, 25, 9, "1.25.9"}, Version{1, 26, 0, "1.26.0"}, -1, false},
		{"greater patch only", Version{1, 26, 6, "1.26.6"}, Version{1, 26, 5, "1.26.5"}, 1, true},
		{"lesser patch only", Version{1, 26, 4, "1.26.4"}, Version{1, 26, 5, "1.26.5"}, -1, false},
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
	v, err := ParseVersion("go version go1.26.5-X:nodwarf5 linux/amd64")
	if err != nil {
		t.Fatalf("ParseVersion returned error: %v", err)
	}
	if got, want := v.String(), "1.26.5"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
