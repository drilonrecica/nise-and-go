package release

import (
	"path/filepath"
	"testing"
)

// The signal that does the real work here was learned by testing this
// project's installation channels rather than by reading them: the release
// build stamps a commit through -ldflags, and `go install` cannot, because it
// compiles from a module zip that carries no version control metadata. These
// cases pin that reasoning so a later change cannot quietly invert it.
func TestDetectIdentifiesEachInstallationChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                          string
		commit, moduleVersion, binary string
		want                          Channel
	}{
		{
			name:          "homebrew, reached through the Cellar",
			commit:        "1a2b3c4d",
			moduleVersion: "v0.1.0",
			binary:        filepath.FromSlash("/opt/homebrew/Cellar/nise/0.1.0/bin/nise"),
			want:          ChannelHomebrew,
		},
		{
			name:          "homebrew on Apple Silicon, symlink unresolved",
			commit:        "1a2b3c4d",
			moduleVersion: "v0.1.0",
			binary:        filepath.FromSlash("/opt/homebrew/bin/nise"),
			want:          ChannelHomebrew,
		},
		{
			name:          "homebrew on Linux",
			commit:        "1a2b3c4d",
			moduleVersion: "v0.1.0",
			binary:        filepath.FromSlash("/home/linuxbrew/.linuxbrew/bin/nise"),
			want:          ChannelHomebrew,
		},
		{
			// The discriminator between these two is only the path: a
			// Homebrew binary *is* the release archive's binary.
			name:          "release archive on the PATH",
			commit:        "1a2b3c4d",
			moduleVersion: "v0.1.0",
			binary:        filepath.FromSlash("/usr/local/bin/nise"),
			want:          ChannelArchive,
		},
		{
			name:          "go install: a module version and no release stamp",
			commit:        "",
			moduleVersion: "v0.1.0",
			binary:        filepath.FromSlash("/home/someone/go/bin/nise"),
			want:          ChannelGoInstall,
		},
		{
			name:          "built from a checkout",
			commit:        "",
			moduleVersion: "(devel)",
			binary:        filepath.FromSlash("/tmp/go-build123/b001/exe/nise"),
			want:          ChannelSource,
		},
		{
			name:          "nothing to go on",
			commit:        "",
			moduleVersion: "",
			binary:        "",
			want:          ChannelUnknown,
		},
		{
			// A stamped binary whose own path cannot be read is still
			// unmistakably a release build; losing the path costs the
			// Homebrew/archive distinction, not the answer.
			name:          "stamped, but the path is unknown",
			commit:        "1a2b3c4d",
			moduleVersion: "",
			binary:        "",
			want:          ChannelArchive,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := detect(c.commit, c.moduleVersion, c.binary); got != c.want {
				t.Errorf("detect(%q, %q, %q) = %q, want %q", c.commit, c.moduleVersion, c.binary, got, c.want)
			}
		})
	}
}

// Homebrew is checked before the release stamp, and it has to be: a Homebrew
// installation carries exactly the stamp a downloaded archive does, so
// checking the stamp first would report every Homebrew user as an archive user
// and print them an instruction that does nothing.
func TestHomebrewIsCheckedBeforeTheReleaseStamp(t *testing.T) {
	t.Parallel()
	got := detect("1a2b3c4d", "v0.1.0", filepath.FromSlash("/opt/homebrew/Cellar/nise/0.1.0/bin/nise"))
	if got != ChannelHomebrew {
		t.Errorf("a stamped binary inside a Homebrew Cellar was detected as %q; a Homebrew binary is the release archive's binary, and only its path tells them apart", got)
	}
}

// Detect reads the running test binary, which is neither installed nor
// released — the point is that it answers rather than panicking or reaching
// for anything it should not.
func TestDetectAnswersForTheRunningBinary(t *testing.T) {
	t.Parallel()
	switch got := Detect(); got {
	case ChannelHomebrew, ChannelArchive, ChannelGoInstall, ChannelSource, ChannelUnknown:
	default:
		t.Errorf("Detect() = %q, which is not one of this package's channels", got)
	}
}
