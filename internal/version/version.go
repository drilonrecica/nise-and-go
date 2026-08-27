// Package version reports the version, commit, and build date of the running
// nise binary.
//
// Release builds stamp the package-level variables with -ldflags -X. Builds
// without ldflags — most importantly `go install
// github.com/drilonrecica/nise-and-go/cmd/nise@vX.Y.Z` — fall back to the
// module and VCS metadata the Go toolchain embeds via
// runtime/debug.ReadBuildInfo, so an installed binary still reports its real
// version instead of "dev". Only a from-source build with no metadata at all
// reports "dev".
//
// This package never calls the network, never reads a file, and never checks
// for updates. It reads only data linked into the binary itself.
package version

import (
	"runtime/debug"
	"strings"
)

// These variables are stamped at release-build time with:
//
//	go build -ldflags "\
//	  -X github.com/drilonrecica/nise-and-go/internal/version.Version=v1.2.3 \
//	  -X github.com/drilonrecica/nise-and-go/internal/version.Commit=<sha> \
//	  -X github.com/drilonrecica/nise-and-go/internal/version.Date=<RFC3339>"
//
// They exist only as link-time injection points; nothing assigns to them at
// runtime. An empty value means "not stamped" and triggers the build-info
// fallback in Get.
var (
	// Version is the release version, e.g. "v1.2.3".
	Version string
	// Commit is the VCS revision the binary was built from.
	Commit string
	// Date is the build or commit timestamp, e.g. RFC 3339 UTC.
	Date string
)

// fallbackVersion is reported when neither ldflags nor build info carry a
// usable version (for example a plain `go build` in a source checkout).
const fallbackVersion = "dev"

// Info describes the build of the running binary. It is the value a future
// --json output mode serializes.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

// Get returns the build information for the running binary, preferring the
// ldflags-stamped variables and falling back to
// runtime/debug.ReadBuildInfo for anything left unset.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	return newInfo(Version, Commit, Date, bi, ok)
}

// newInfo merges the stamped values with the embedded build info. It is split
// from Get so the fallback logic is testable with a fabricated
// debug.BuildInfo.
func newInfo(version, commit, date string, bi *debug.BuildInfo, ok bool) Info {
	info := Info{Version: version, Commit: commit, Date: date}
	if ok && bi != nil {
		if info.Version == "" {
			// bi.Main.Version is the module version for `go install
			// module@version` builds ("v1.2.3" or a pseudo-version) and
			// "(devel)" for builds from a source checkout.
			if v := bi.Main.Version; v != "" && v != "(devel)" {
				info.Version = v
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = fallbackVersion
	}
	return info
}

// String returns the one-line human-readable version, e.g.
//
//	nise v1.2.3 (commit 1a2b3c4d, built 2026-08-27T00:00:00Z)
//
// Parts that are unknown are omitted; the minimum form is "nise dev".
func (i Info) String() string {
	var b strings.Builder
	b.WriteString("nise ")
	b.WriteString(i.Version)
	var meta []string
	if i.Commit != "" {
		meta = append(meta, "commit "+i.Commit)
	}
	if i.Date != "" {
		meta = append(meta, "built "+i.Date)
	}
	if len(meta) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(meta, ", "))
		b.WriteString(")")
	}
	return b.String()
}
