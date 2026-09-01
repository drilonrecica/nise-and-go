package release

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/version"
)

// Channel is how a nise binary reached the machine it is running on. It
// exists so an update check can print the one instruction that will work,
// rather than three and a shrug.
type Channel string

const (
	// ChannelHomebrew is an install through the official tap. Its binary is
	// the release archive's, so the only thing that distinguishes it is where
	// it sits on disk.
	ChannelHomebrew Channel = "homebrew"
	// ChannelArchive is a release archive downloaded from GitHub Releases and
	// put on the PATH by hand.
	ChannelArchive Channel = "archive"
	// ChannelGoInstall is `go install github.com/…/cmd/nise@vX.Y.Z`.
	ChannelGoInstall Channel = "go-install"
	// ChannelSource is a build from a checkout of this repository. There is
	// nothing to update; there is a branch to pull.
	ChannelSource Channel = "source"
	// ChannelUnknown is every other case, including one where the running
	// binary's own path cannot be determined. The command prints every
	// instruction rather than guessing, because a wrong instruction is worse
	// than an unanswered question.
	ChannelUnknown Channel = "unknown"
)

// homebrewMarkers are path fragments that identify a Homebrew installation on
// the platforms Homebrew supports. `Cellar` is the reliable one — every
// formula's files live under it — and the prefixes cover a binary reached
// through the `bin` symlink on a system whose os.Executable does not resolve
// it.
var homebrewMarkers = []string{
	string(filepath.Separator) + "Cellar" + string(filepath.Separator),
	filepath.FromSlash("/opt/homebrew/"),
	filepath.FromSlash("/usr/local/Homebrew/"),
	filepath.FromSlash("/home/linuxbrew/"),
	filepath.FromSlash("/.linuxbrew/"),
}

// Detect reports how the running binary was most likely installed.
//
// The signal that does the real work is one this project only learned by
// testing its own installation channels: the release build stamps a commit
// through -ldflags, and `go install` cannot — it compiles from a module zip,
// which carries no version control metadata for the toolchain to record. So a
// stamped commit means the binary came out of the release workflow, and its
// absence beside a real module version means it was compiled on this machine.
//
// Homebrew is checked first, because a Homebrew binary *is* the release
// archive's binary; only its location on disk tells them apart.
func Detect() Channel {
	return detect(version.Commit, moduleVersion(), executablePath())
}

// detect is Detect's pure core, so every branch is testable without an
// installed nise.
func detect(stampedCommit, moduleVersion, executable string) Channel {
	if executable != "" {
		for _, marker := range homebrewMarkers {
			if strings.Contains(executable, marker) {
				return ChannelHomebrew
			}
		}
	}
	if stampedCommit != "" {
		// Stamped by the release build, and not sitting in a Homebrew tree.
		return ChannelArchive
	}
	// A module version the toolchain recorded, with no release stamp: built
	// from the module proxy on this machine.
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return ChannelGoInstall
	}
	if moduleVersion == "(devel)" {
		return ChannelSource
	}
	return ChannelUnknown
}

// executablePath returns the running binary's path, or "" when it cannot be
// determined. A missing path is not an error worth surfacing: it costs the
// caller a channel-specific instruction, not the update check itself.
//
// This is the one os.Executable call in this repository, and
// test/nonetwork/selfupdate_test.go names it. It reads the path; nothing here
// writes to it, and no code path in nise writes to a binary at all.
func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	// A symlink is what Homebrew puts on the PATH. Resolving it is what turns
	// <prefix>/bin/nise into <prefix>/Cellar/nise/<version>/bin/nise, which is
	// the form homebrewMarkers recognizes on every platform.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// moduleVersion returns the module version the Go toolchain embedded, which
// is "vX.Y.Z" for a `go install module@version` build and "(devel)" for a
// build from a source checkout.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return ""
	}
	return info.Main.Version
}
