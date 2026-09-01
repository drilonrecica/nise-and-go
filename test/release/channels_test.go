package release_test

// Nise is installable three ways, and the interesting failure is not a broken
// channel — that one is loud — but two channels that work and disagree.
// .github/workflows/install-channels.yml installs a published release the way
// three different people would and then compares what they got; these tests
// pin the properties that make that comparison mean something.

import (
	"regexp"
	"strings"
	"testing"
)

// The documented installation channels. Written out here for the same reason
// releaseTargets is: a test derived from the workflow it checks agrees with it
// by construction.
var installChannels = map[string][]string{
	"archive":    {"ubuntu-latest", "macos-latest", "windows-latest"},
	"go-install": {"ubuntu-latest", "macos-latest", "windows-latest"},
	// Homebrew does not run on Windows.
	"homebrew": {"ubuntu-latest", "macos-latest"},
}

func channelsWorkflow(t *testing.T) string {
	t.Helper()
	return readFile(t, ".github/workflows/install-channels.yml")
}

// The "v" in the stamped version is load-bearing, and it is one character in
// a YAML file with nothing else guarding it.
//
// GoReleaser's {{ .Version }} is the tag with its "v" stripped, which is right
// for a file name and wrong for the version the binary reports: the other
// channel is `go install module@v0.1.0`, whose version comes from
// debug.ReadBuildInfo and is "v0.1.0". Stamping the bare number made one
// release report two different version strings depending on how it was
// installed — and since `nise new` writes that string into the project recipe
// verbatim, it made one release generate two different project trees.
func TestTheReleaseStampsTheVersionInTheFormEveryChannelReports(t *testing.T) {
	t.Parallel()
	source := goreleaserConfig(t)

	stamp := "internal/version.Version=v{{ .Version }}"
	if !strings.Contains(source, stamp) {
		t.Errorf("the release does not stamp %s; a bare {{ .Version }} makes the archive channel disagree with `go install`", stamp)
	}
	// The archive *names* keep the bare number, and are documented that way.
	// This is the check that stops the fix above from being applied in the
	// wrong place.
	if !strings.Contains(source, `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`) {
		t.Error("the archive name template changed; archive names carry the bare version and the stamped version carries the v")
	}
}

// Every channel a reader is told about is installed, on every platform that
// channel serves. A channel dropped from the workflow would stop being tested
// with nothing failing.
func TestEveryDocumentedChannelIsInstalled(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)

	for channel, runners := range installChannels {
		job := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(channel) + `:\s*$`)
		if !job.MatchString(workflow) {
			t.Errorf("install-channels.yml has no %s job, so that channel is never installed", channel)
			continue
		}
		for _, runner := range runners {
			if !strings.Contains(workflow, runner) {
				t.Errorf("the %s channel is documented for %s and the workflow never runs there", channel, runner)
			}
		}
	}
	// Homebrew has no Windows runner, and a copied matrix would give it one
	// — a job that fails on every run and gets disabled rather than fixed.
	homebrew := workflow[strings.Index(workflow, "  homebrew:"):]
	homebrew = homebrew[:strings.Index(homebrew, "  agree:")]
	if strings.Contains(homebrew, "windows-latest") {
		t.Error("the Homebrew job runs on Windows, where Homebrew does not exist")
	}
}

// The documented commands, run as written. A workflow that installs some other
// way tests something nobody was told to do.
func TestTheWorkflowRunsTheDocumentedInstallCommands(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)
	doc := readFile(t, "docs/cli-and-distribution.md")

	for _, command := range []string{
		"brew install drilonrecica/tap/nise",
		"go install github.com/drilonrecica/nise-and-go/cmd/nise@",
	} {
		if !strings.Contains(workflow, command) {
			t.Errorf("install-channels.yml never runs %q", command)
		}
		if !strings.Contains(doc, command) {
			t.Errorf("docs/cli-and-distribution.md does not document %q, which the workflow tests", command)
		}
	}
	// The archive path is the documented verification, not an ad-hoc one.
	if !strings.Contains(workflow, "sha256sum --check --ignore-missing checksums.txt") {
		t.Error("the archive channel does not verify the download the way the documentation tells a person to")
	}
}

// A release binary that needs a Go toolchain to run is not a release binary.
// The only way to test that is to install it on a runner that has none.
func TestTheArchiveChannelInstallsWithoutAToolchain(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)

	start := strings.Index(workflow, "  archive:")
	end := strings.Index(workflow, "  go-install:")
	if start < 0 || end < 0 || end < start {
		t.Fatal("install-channels.yml has no archive job followed by a go-install job")
	}
	if strings.Contains(workflow[start:end], "actions/setup-go") {
		t.Error("the archive channel sets up Go, so it no longer tests that a release binary is standalone")
	}
	// And the channel that legitimately compiles on the installing machine
	// does set one up, or it is testing nothing at all.
	if !strings.Contains(workflow[end:], "actions/setup-go") {
		t.Error("the go install channel sets up no Go toolchain")
	}
}

// Installing is not the claim; working is. Each channel creates a project
// with what it installed.
func TestEveryChannelIsMadeToDoTheThingItWasInstalledFor(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)

	if got := strings.Count(workflow, "new channel-check --yes --json"); got != len(installChannels) {
		t.Errorf("%d of %d channels create a project with what they installed", got, len(installChannels))
	}
}

// The comparison runs even when a channel failed, and treats a channel that
// reported nothing as a failure rather than as one fewer thing to compare.
func TestTheComparisonRunsEvenWhenAChannelFailed(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)

	agree := workflow[strings.Index(workflow, "  agree:"):]
	if !strings.Contains(agree, "if: always()") {
		t.Error("the agreement job is skipped when a channel fails, so a broken channel produces no comparison at all")
	}
	for channel := range installChannels {
		if !strings.Contains(agree, channel) {
			t.Errorf("the agreement job does not depend on the %s channel", channel)
		}
	}
	if !strings.Contains(agree, "go run ./cmd/channel-agreement") {
		t.Error("the agreement job does not run the comparison this repository tests")
	}
}

// A channel does not only break at release time: the tap is another
// repository and the module proxy is somebody else's cache, and either can
// stop serving a release that worked on the day it was published.
func TestTheChannelsAreRecheckedOnASchedule(t *testing.T) {
	t.Parallel()
	workflow := channelsWorkflow(t)

	if !strings.Contains(workflow, "schedule:") || !strings.Contains(workflow, "cron:") {
		t.Error("install-channels.yml runs only at release time, so a channel that breaks later breaks silently")
	}
	// A scheduled run has no event tag and no input; it must resolve one.
	if !strings.Contains(workflow, "gh release list") {
		t.Error("the scheduled run has no tag to install and resolves none")
	}
}

// The workflow's channel list and the comparator's must be the same list.
func TestTheComparatorKnowsTheSameChannels(t *testing.T) {
	t.Parallel()
	source := readFile(t, "cmd/channel-agreement/main.go")

	for channel, runners := range installChannels {
		if !strings.Contains(source, `{"`+channel+`"`) {
			t.Errorf("cmd/channel-agreement does not compare the %s channel", channel)
			continue
		}
		for _, runner := range runners {
			if !strings.Contains(source, `"`+runner+`"`) {
				t.Errorf("cmd/channel-agreement never expects a report from %s", runner)
			}
		}
	}
	declared := regexp.MustCompile(`(?m)^\t\{"([a-z-]+)", \[\]string\{`).FindAllStringSubmatch(source, -1)
	if len(declared) != len(installChannels) {
		t.Errorf("cmd/channel-agreement compares %d channels, and this project supports %d", len(declared), len(installChannels))
	}
}
