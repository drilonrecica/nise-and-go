package doctor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a loosely-parsed major.minor.patch version, extracted from
// whatever a real tool's `--version` (or equivalent) flag happens to
// print. It deliberately does not implement full semver: a vendor suffix
// or prerelease tag (see versionPattern's doc comment) is preserved in Raw
// for display but ignored for comparison, because the tools doctor checks
// are not required to speak strict semver and doctor's only job is "new
// enough", not "exactly this build".
type Version struct {
	Major, Minor, Patch int
	// Raw is the exact substring ParseVersion matched, e.g. "1.26.5" out
	// of "go version go1.26.5-X:nodwarf5 linux/amd64". Kept for display so
	// a check's "found" text shows the real string a developer can search
	// for, not a value doctor reconstructed.
	Raw string
}

// versionPattern matches the first dotted numeric version core in a
// string: one or two required dots, no leading sign, greedy digit runs.
// It intentionally does not try to also capture a prerelease/build suffix
// (e.g. "-rc1", "+build.5", or a vendor tag like "-X:nodwarf5" in this
// machine's own `go version` output) — comparison only ever needs
// major.minor.patch, and matching a suffix would risk swallowing the
// vendor tag of a tool that puts one directly after the version with no
// separating word, which varies per tool and per platform. Whatever
// follows the numeric core is simply left out of the match and ignored.
var versionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?`)

// ParseVersion extracts the first major.minor.patch version core found
// anywhere in s and returns it as a Version. It tolerates a "go", "v", or
// other leading label (go1.26.5, v1.2.3), a missing patch component
// (1.2, treated as 1.2.0), a trailing prerelease or build tag (1.2.3-rc1,
// 2.8.0+meta — the tag is discarded), and surrounding prose of any kind
// (Docker version 29.6.2, build dfc4efb; podman version 5.8.4; goose
// version: v3.27.3; multi-line oapi-codegen output that names its own
// import path before printing a version on a second line). It returns an
// error when s contains nothing shaped like a version, or when a matched
// numeric component does not fit an int (a hostile, absurdly large
// version string).
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindString(s)
	if m == "" {
		return Version{}, fmt.Errorf("no version number found in %q", strings.TrimSpace(s))
	}
	parts := strings.Split(m, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: major component out of range: %w", m, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: minor component out of range: %w", m, err)
	}
	patch := 0
	if len(parts) > 2 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return Version{}, fmt.Errorf("version %q: patch component out of range: %w", m, err)
		}
	}
	return Version{Major: major, Minor: minor, Patch: patch, Raw: m}, nil
}

// Compare returns -1, 0, or 1 as v is less than, equal to, or greater than
// other, comparing Major, then Minor, then Patch. It never looks at Raw:
// two versions parsed from differently-formatted strings with the same
// numeric core compare equal.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return compareInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return compareInt(v.Minor, other.Minor)
	}
	return compareInt(v.Patch, other.Patch)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether v is greater than or equal to min.
func (v Version) AtLeast(min Version) bool {
	return v.Compare(min) >= 0
}

// String returns the exact substring Version was parsed from.
func (v Version) String() string {
	return v.Raw
}
