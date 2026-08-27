package doctor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a loosely-parsed major.minor.patch version, extracted from
// whatever a real tool's `--version` (or equivalent) flag happens to
// print. It deliberately does not implement full semver: an arbitrary
// vendor suffix (see versionPattern's doc comment) is preserved in Raw for
// display but ignored for comparison, because the tools doctor checks are
// not required to speak strict semver and doctor's only job is "new
// enough", not "exactly this build".
//
// A recognized pre-release marker (rc, alpha, beta, pre, or dev — see
// prereleasePattern) is the one exception: it is not just cosmetic. A
// pre-release build is, by definition, not yet the release it names, so
// Compare treats a version with a non-empty Prerelease as strictly less
// than the same major.minor.patch with an empty one — matching semver's
// own precedence rule (a pre-release sorts below its associated normal
// version) and, concretely, making `go1.26rc1` fail an `AtLeast(1.26.0)`
// check rather than satisfy it.
type Version struct {
	Major, Minor, Patch int
	// Raw is the exact substring ParseVersion matched — the numeric core
	// plus a recognized pre-release tag when one immediately follows it,
	// e.g. "1.26.5" out of "go version go1.26.5-X:nodwarf5 linux/amd64"
	// (the vendor tag is not a recognized pre-release marker, so it is
	// not included), or "1.26rc1" out of "go version go1.26rc1
	// linux/amd64" (rc1 is recognized, so it is included). Kept for
	// display so a check's "found" text shows the real string a
	// developer can search for, not a value doctor reconstructed.
	Raw string
	// Prerelease is the recognized pre-release tag (e.g. "rc1", "beta2"),
	// lowercased, or "" when none was found. Only used by Compare; never
	// re-derived from Raw.
	Prerelease string
}

// versionPattern matches the first dotted numeric version core in a
// string: one or two required dots, no leading sign, greedy digit runs.
// It intentionally does not try to also capture an arbitrary build/vendor
// suffix (e.g. a vendor tag like "-X:nodwarf5" in this machine's own `go
// version` output) — matching one would risk swallowing whatever
// arbitrary text a given tool or platform glues on, which varies per tool
// and has no fixed shape. What immediately follows the numeric core is
// separately checked against prereleasePattern (a small, fixed set of
// known pre-release keywords); anything else is left out of the match and
// ignored, exactly as before.
var versionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?`)

// prereleasePattern matches a recognized pre-release marker directly
// after a version core, with or without a separator. This is what makes
// Go's own release-candidate spelling parse correctly: Go writes
// "go1.26rc1" with **no** separator before "rc" (unlike the hyphenated
// semver convention, "1.2.3-rc1", that a pattern written only for that
// convention would expect) — versionPattern's match on "go1.26rc1" stops
// at "1.26" because "r" is not a digit or dot, so without this second,
// explicit check for a keyword immediately following, the "rc1" marker
// would simply be dropped: ParseVersion would silently report a normal
// 1.26.0, and a release candidate would satisfy AtLeast(1.26.0) — the
// exact false-ok this whole package exists to prevent.
//
// The keyword list (rc, alpha, beta, pre, dev) is deliberately narrow and
// fixed, not "any following word": a build/vendor tag like "-X:nodwarf5"
// or "+build.5" must NOT be mistaken for a pre-release marker, or a
// perfectly good release build would be incorrectly demoted below its own
// release version. Matching is case-insensitive (semver pre-release
// identifiers are conventionally lowercase, but tolerate a differently
// cased tool without failing to recognize it).
var prereleasePattern = regexp.MustCompile(`(?i)^[-+._]?(?:rc|alpha|beta|pre|dev)[0-9]*`)

// ParseVersion extracts the first major.minor.patch version core found
// anywhere in s and returns it as a Version. It tolerates a "go", "v", or
// other leading label (go1.26.5, v1.2.3), a missing patch component (1.2,
// treated as 1.2.0), a recognized pre-release tag immediately following
// the core with or without a separator (1.2.3-rc1, go1.26rc1 — see
// prereleasePattern), an unrecognized trailing build/vendor tag (2.8.0
// +meta, 1.2.3-X:nodwarf5 — discarded, and never mistaken for a
// pre-release), and surrounding prose of any kind (Docker version
// 29.6.2, build dfc4efb; podman version 5.8.4; goose version: v3.27.3;
// multi-line oapi-codegen output that names its own import path before
// printing a version on a second line). It returns an error when s
// contains nothing shaped like a version, or when a matched numeric
// component does not fit an int (a hostile, absurdly large version
// string).
func ParseVersion(s string) (Version, error) {
	loc := versionPattern.FindStringIndex(s)
	if loc == nil {
		return Version{}, fmt.Errorf("no version number found in %q", strings.TrimSpace(s))
	}
	m := s[loc[0]:loc[1]]
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

	raw := m
	prerelease := ""
	if suf := prereleasePattern.FindString(s[loc[1]:]); suf != "" {
		raw += suf
		prerelease = strings.ToLower(strings.TrimLeft(suf, "-+._"))
	}

	return Version{Major: major, Minor: minor, Patch: patch, Raw: raw, Prerelease: prerelease}, nil
}

// Compare returns -1, 0, or 1 as v is less than, equal to, or greater than
// other, comparing Major, then Minor, then Patch, then — only when those
// three are equal — Prerelease, per semver's own precedence rule: a
// version with a pre-release tag is less than the same major.minor.patch
// without one. It never looks at Raw: two versions parsed from
// differently-formatted strings with the same numeric core and the same
// pre-release status compare equal.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return compareInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return compareInt(v.Minor, other.Minor)
	}
	if v.Patch != other.Patch {
		return compareInt(v.Patch, other.Patch)
	}
	return comparePrerelease(v.Prerelease, other.Prerelease)
}

// comparePrerelease orders a pre-release tag as less than no tag at all
// (an empty string), matching semver's "pre-release has lower precedence
// than the associated normal version" rule. When both sides carry a
// pre-release tag, they are compared lexicographically — a deliberate
// simplification of semver's own multi-part, numeric-aware pre-release
// comparison algorithm, which doctor has no need for: every check in this
// package only ever compares a real tool's reported version against a
// plain release floor (which never itself carries a pre-release tag), so
// this branch exists for completeness and to keep Compare a total order,
// not because doctor needs to rank one pre-release against another.
func comparePrerelease(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
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

// String returns the exact substring Version was parsed from, including a
// recognized pre-release tag when one was found (see Raw).
func (v Version) String() string {
	return v.Raw
}
