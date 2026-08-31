package golden_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

// The determinism gate for `nise generate` (Nise task M7-010).
//
// `nise new`'s output is pinned by the manifests in this package's testdata.
// A generated feature is different in kind: it is written into a project that
// already exists, so there is no committed tree to compare against. What can
// be pinned — and what actually matters — is that the output is a pure
// function of its inputs: two runs with equal options produce byte-identical
// files with identical modes, whatever the machine, the working directory, the
// clock, the locale, or the umask says.
//
// That is the property the whole command rests on. A generator whose output
// varied would make every regeneration a diff to review, and would make ADR
// 0026's refusal-on-collision meaningless: "this file already exists" is only
// useful advice if the file that exists is the one this run would have written.

// featureVariants are the shapes this suite pins: both kinds, and a name whose
// plural is not formed by adding an s, because that is where a spelling
// derivation is most likely to differ between two implementations of the same
// rule.
func featureVariants() []struct {
	Name    string
	Options feature.Options
} {
	return []struct {
		Name    string
		Options feature.Options
	}{
		{
			Name: "feature",
			Options: feature.Options{
				Kind:       feature.KindFeature,
				Name:       "invoice",
				ModulePath: "github.com/example/myapp",
				AppName:    "myapp",
			},
		},
		{
			Name: "resource",
			Options: feature.Options{
				Kind:          feature.KindResource,
				Name:          "order",
				ModulePath:    "github.com/example/myapp",
				AppName:       "myapp",
				NextMigration: 9,
			},
		},
		{
			Name: "irregular plural",
			Options: feature.Options{
				Kind:          feature.KindResource,
				Name:          "category",
				ModulePath:    "github.com/example/myapp",
				AppName:       "myapp",
				NextMigration: 12,
			},
		},
	}
}

// TestFeatureGenerationIsByteForByteDeterministic writes each variant twice,
// into unrelated directories, and compares the whole tree: paths, modes, and
// contents.
//
// It compares written trees rather than the in-memory plan, so the path
// separators, the directory creation, and the file modes are all covered — a
// mode inherited from the process umask is exactly the kind of ambient state
// that would make one machine's output differ from another's.
func TestFeatureGenerationIsByteForByteDeterministic(t *testing.T) {
	t.Parallel()

	for _, v := range featureVariants() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()

			first := writeFeature(t, v.Options, v.Name)
			second := writeFeature(t, v.Options, v.Name)
			if first != second {
				t.Errorf("two runs produced different trees:\n--- first ---\n%s\n--- second ---\n%s", first, second)
			}
		})
	}
}

// TestFeatureGenerationIgnoresTheEnvironment covers the channels a generator
// accidentally reads from: the working directory, the clock's zone, and the
// locale. The umask is covered separately, in a build-tagged file, because
// syscall.Umask does not exist on Windows.
func TestFeatureGenerationIgnoresTheEnvironment(t *testing.T) {
	// Not parallel: chdir and the environment are process-wide.
	v := featureVariants()[1]

	baseline := writeFeature(t, v.Options, v.Name)

	t.Chdir(t.TempDir())
	t.Setenv("TZ", "Pacific/Kiritimati")
	t.Setenv("LANG", "tr_TR.UTF-8")
	t.Setenv("LC_ALL", "tr_TR.UTF-8")

	if got := writeFeature(t, v.Options, v.Name); got != baseline {
		t.Errorf("the environment changed the generated tree:\n--- baseline ---\n%s\n--- hostile ---\n%s", baseline, got)
	}
}

// TestFeatureGenerationCarriesNoEnvironmentTraces is the direct check: nothing
// from the machine that ran the generator reaches the output.
func TestFeatureGenerationCarriesNoEnvironmentTraces(t *testing.T) {
	t.Parallel()

	for _, v := range featureVariants() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			plan, err := feature.NewPlan(v.Options)
			if err != nil {
				t.Fatalf("NewPlan: %v", err)
			}
			if _, err := feature.Write(root, plan); err != nil {
				t.Fatalf("Write: %v", err)
			}

			for _, file := range plan.Files {
				body := string(file.Content)
				// A contributor cloning this repository on Windows with
				// core.autocrlf enabled gets CRLF templates; without
				// normalization every generated feature on that machine would
				// differ from every other machine's.
				if strings.Contains(body, "\r") {
					t.Errorf("%s contains a carriage return", file.Path)
				}
				if !strings.HasSuffix(body, "\n") || strings.HasSuffix(body, "\n\n") {
					t.Errorf("%s does not end with exactly one newline", file.Path)
				}
				for _, needle := range environmentStrings(t, root) {
					if needle != "" && strings.Contains(body, needle) {
						t.Errorf("%s contains %q, which came from this machine", file.Path, needle)
					}
				}
				if timestampPattern.MatchString(body) {
					t.Errorf("%s contains a timestamp: %q", file.Path, timestampPattern.FindString(body))
				}
			}
		})
	}
}

// writeFeature writes one variant into a fresh directory and returns its
// manifest: every path with its mode and content hash.
func writeFeature(t *testing.T, options feature.Options, variantName string) string {
	t.Helper()

	plan, err := feature.NewPlan(options)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := feature.Write(root, plan); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return treeManifest(t, root, variantName)
}
