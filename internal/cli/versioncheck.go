package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/doctor"
	"github.com/drilonrecica/nise-and-go/internal/release"
	"github.com/drilonrecica/nise-and-go/internal/version"
)

// `nise version check` is the only command in nise that opens a network
// connection on the user's behalf, and it is named to make that obvious in
// both directions: it is a *check*, and it is about the *version*.
//
// It is deliberately not called `nise update`. There is already a `nise
// upgrade`, which upgrades a generated project, and two near-synonyms meaning
// different things is a worse problem than a longer name. It is also not
// called update because it does not update: it prints the command you would
// run, for the way you installed nise, and then stops. Nise never modifies a
// binary — least of all one a package manager owns.
const versionCheckDocs = "docs/cli-and-distribution.md"

// upgradeInstructions is the command to run for each installation channel.
//
// The point of detecting the channel at all is to print one of these rather
// than all of them. A page of alternatives is a page the reader has to
// disambiguate, usually while already mildly annoyed that their nise is old.
func upgradeInstructions(channel release.Channel, tag string) []string {
	switch channel {
	case release.ChannelHomebrew:
		return []string{"brew update && brew upgrade nise"}
	case release.ChannelGoInstall:
		return []string{"go install github.com/drilonrecica/nise-and-go/cmd/nise@" + tag}
	case release.ChannelArchive:
		return []string{
			"Download " + tag + " from the release page below, verify it, and replace the binary on your PATH.",
			"gh release download " + tag + " --repo drilonrecica/nise-and-go",
		}
	case release.ChannelSource:
		return []string{
			"This nise was built from a checkout, so there is nothing to install:",
			"git -C <your checkout> fetch --tags && git checkout " + tag,
		}
	default:
		// An honest "I could not tell" beats a confident wrong instruction:
		// running `brew upgrade` on a binary Homebrew does not own does
		// nothing and reads like a bug in nise.
		return []string{
			"nise could not tell how it was installed. Use whichever of these matches:",
			"brew update && brew upgrade nise",
			"go install github.com/drilonrecica/nise-and-go/cmd/nise@" + tag,
			"gh release download " + tag + " --repo drilonrecica/nise-and-go",
		}
	}
}

// versionCheckResult is what the command reports, in both renderings.
type versionCheckResult struct {
	Installed string   `json:"installed"`
	Latest    string   `json:"latest"`
	Channel   string   `json:"channel"`
	Outdated  bool     `json:"outdated"`
	URL       string   `json:"url,omitempty"`
	HowTo     []string `json:"howTo,omitempty"`
}

func (r versionCheckResult) Human() string {
	var b strings.Builder
	if !r.Outdated {
		fmt.Fprintf(&b, "nise %s is the latest release.", r.Installed)
		return b.String()
	}

	fmt.Fprintf(&b, "nise %s is installed; %s is the latest release.\n", r.Installed, r.Latest)
	fmt.Fprintf(&b, "\nInstalled through: %s\n", r.Channel)
	for _, line := range r.HowTo {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	if r.URL != "" {
		fmt.Fprintf(&b, "\nRelease notes: %s\n", r.URL)
	}
	b.WriteString("\nnise changed nothing. It never installs or replaces a binary.")
	return b.String()
}

func versionCheckCommand() *Command {
	// -url exists for this repository's own tests, which point it at a local
	// server. It is not documented as a user-facing option for the reason its
	// usage string gives: a check that can be aimed anywhere is a check whose
	// answer means nothing, and the default is the only value anybody should
	// use.
	var baseURL string
	return &Command{
		Name:  "check",
		Short: "Check whether a newer nise release exists (the only command that uses the network)",
		NewFlagSet: func() *flag.FlagSet {
			fs := flag.NewFlagSet("version check", flag.ContinueOnError)
			fs.StringVar(&baseURL, "url", "", "release index to query instead of GitHub (for this repository's own tests)")
			return fs
		},
		Run: func(ctx context.Context, env *Env) error {
			installed := version.Get().Version
			channel := release.Detect()

			// The one command in nise that opens a socket says so, and names
			// where, before it does. Suppressed by --quiet and in JSON mode,
			// like every other informational line.
			env.Out.Line("checking github.com/drilonrecica/nise-and-go for a newer release")

			latest, err := release.Fetch(ctx, baseURL)
			if err != nil {
				return versionCheckError(err)
			}

			outdated, err := isOlder(installed, latest.Tag)
			if err != nil {
				return err
			}

			result := versionCheckResult{
				Installed: installed,
				Latest:    latest.Tag,
				Channel:   string(channel),
				Outdated:  outdated,
			}
			if outdated {
				result.URL = latest.URL
				result.HowTo = upgradeInstructions(channel, latest.Tag)
			}
			env.Out.Result(result)
			return nil
		},
	}
}

// isOlder compares the running version against the latest release tag,
// reusing internal/doctor's parser so that the CLI has one idea of what a
// version is rather than two that drift.
//
// A version this parser cannot read is not a failure: a binary built from a
// checkout reports "dev", and telling that person the latest release exists is
// the useful answer. It is reported as outdated only when it is genuinely
// comparable and genuinely older.
func isOlder(installed, latestTag string) (bool, error) {
	latest, err := doctor.ParseVersion(latestTag)
	if err != nil {
		return false, clierr.Wrap(err, clierr.ExitError,
			fmt.Sprintf("the latest release is tagged %q, which is not a version nise can compare", latestTag),
			"Report this: the release index answered with a tag that is not a release tag.",
		).WithCode("version_check.unreadable_tag")
	}
	running, err := doctor.ParseVersion(installed)
	if err != nil {
		// "dev" and other non-comparable builds: there is a published
		// release, and this binary is not it.
		return true, nil
	}
	return !running.AtLeast(latest), nil
}

// versionCheckError turns a fetch failure into an error that says what nise
// was doing and what the reader can do about it. The recovery line matters
// more here than almost anywhere else in the CLI: the most common cause is
// simply having no network, and a person who ran one command that needs one
// should not be left wondering which.
func versionCheckError(err error) error {
	if errors.Is(err, release.ErrNoRelease) {
		return clierr.Wrap(err, clierr.ExitError,
			"there is no published nise release to compare against",
			"Nothing is wrong with your installation. Check https://github.com/drilonrecica/nise-and-go/releases when the first release is published.",
		).WithCode("version_check.no_release").WithDocs(versionCheckDocs)
	}
	return clierr.Wrap(err, clierr.ExitError,
		"nise could not reach the release index",
		"Check your network connection and try again. This is the only nise command that needs one; everything else works offline.",
	).WithCode("version_check.unreachable").WithDocs(versionCheckDocs)
}
