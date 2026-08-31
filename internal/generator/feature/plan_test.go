package feature_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

func featureOptions() feature.Options {
	return feature.Options{
		Kind:       feature.KindFeature,
		Name:       "invoice",
		ModulePath: "example.com/demo",
	}
}

func planContent(t *testing.T, opts feature.Options) map[string]string {
	t.Helper()

	plan, err := feature.NewPlan(opts)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	content := make(map[string]string, len(plan.Files))
	for _, file := range plan.Files {
		content[file.Path] = string(file.Content)
	}
	return content
}

// TestPlanIsDeterministic is the gate the whole generator rests on: two runs
// with equal options produce byte-identical output, in the same order.
func TestPlanIsDeterministic(t *testing.T) {
	t.Parallel()

	first, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	second, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	if len(first.Files) != len(second.Files) {
		t.Fatalf("file counts differ: %d and %d", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].Path != second.Files[i].Path {
			t.Fatalf("file %d: paths differ: %q and %q", i, first.Files[i].Path, second.Files[i].Path)
		}
		if string(first.Files[i].Content) != string(second.Files[i].Content) {
			t.Errorf("%s: contents differ between two runs", first.Files[i].Path)
		}
	}
}

// TestGeneratedFeatureIsApplicationOwned pins ADR 0026's first rule: every
// file a slice produces belongs to the application from the moment it is
// written, and says so in its own header.
func TestGeneratedFeatureIsApplicationOwned(t *testing.T) {
	t.Parallel()

	plan, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	for _, file := range plan.Files {
		if file.Owner != "app" {
			t.Errorf("%s is owned by %q; a generated slice is application-owned", file.Path, file.Owner)
		}
		if !strings.Contains(string(file.Content), "owned by this application") {
			t.Errorf("%s does not declare its ownership in a header", file.Path)
		}
	}
}

// TestPlanUsesEveryDerivedSpellingCorrectly is the check that the singular and
// plural land where the layout rules say they do: a singular package and
// directory, a plural type name for the use case, and no leftover template.
func TestPlanUsesEveryDerivedSpellingCorrectly(t *testing.T) {
	t.Parallel()

	content := planContent(t, featureOptions())

	usecase, exists := content["internal/features/invoice/usecase.go"]
	if !exists {
		t.Fatal("the plan has no usecase.go")
	}
	for _, fragment := range []string{
		"package invoice",
		"type Invoices struct",
		"func NewInvoices(transactor *database.Transactor) (*Invoices, error)",
		// The permission check is in the use case, not in a handler.
		"authorization.Require(ctx, authorization.InvoicesManage)",
		// The use case owns the transaction.
		"u.transactor.Within(ctx, transaction.Options{}",
	} {
		if !strings.Contains(usecase, fragment) {
			t.Errorf("usecase.go lacks %q", fragment)
		}
	}

	for path, body := range content {
		if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
			t.Errorf("%s still contains an unrendered template action", path)
		}
	}
}

// TestPlanRefusesWhatItCannotName is the failure path: an unusable name or a
// missing module path stops before anything is planned, rather than producing
// a tree named after it.
func TestPlanRefusesWhatItCannotName(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*feature.Options){
		"empty name":        func(o *feature.Options) { o.Name = "" },
		"uncanonical name":  func(o *feature.Options) { o.Name = "Invoice" },
		"no module path":    func(o *feature.Options) { o.ModulePath = "" },
		"unknown kind":      func(o *feature.Options) { o.Kind = "widget" },
		"underscored name":  func(o *feature.Options) { o.Name = "invoice_line" },
		"non-ASCII in name": func(o *feature.Options) { o.Name = "faktúra" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := featureOptions()
			mutate(&opts)
			if _, err := feature.NewPlan(opts); err == nil {
				t.Fatalf("NewPlan accepted %s", name)
			}
		})
	}
}

// TestPlanPrintsTheInsertionsItRefusesToMake is ADR 0026's second rule: the
// command names every edit a person makes, because it makes none itself.
func TestPlanPrintsTheInsertionsItRefusesToMake(t *testing.T) {
	t.Parallel()

	plan, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if len(plan.Insertions) == 0 {
		t.Fatal("the plan names no insertions; a slice that needs no wiring is not wired to anything")
	}
	for _, insertion := range plan.Insertions {
		if insertion.File == "" || insertion.Anchor == "" || insertion.Snippet == "" {
			t.Errorf("incomplete insertion: %#v", insertion)
		}
		// Every insertion is into a file the application owns; an insertion
		// into a file nise owns would mean nise should have written it.
		for _, file := range plan.Files {
			if file.Path == insertion.File {
				t.Errorf("%s is both generated and named as a manual insertion", insertion.File)
			}
		}
	}
}

// TestWriteRefusesToTouchAnythingThatExists proves the refusal is checked
// before anything is written, so a colliding run leaves the project exactly as
// it found it rather than half-written.
func TestWriteRefusesToTouchAnythingThatExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	plan, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	// One file of the four already exists, with contents of its own.
	collision := plan.Files[len(plan.Files)-1].Path
	target := filepath.Join(root, filepath.FromSlash(collision))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	const mine = "// mine\n"
	if err := os.WriteFile(target, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := feature.Write(root, plan)
	if !errors.Is(err, feature.ErrExists) {
		t.Fatalf("Write = %v, want feature.ErrExists", err)
	}
	if len(written) != 0 {
		t.Errorf("Write reported %v as written; a colliding plan must write nothing", written)
	}
	if got, _ := os.ReadFile(target); string(got) != mine {
		t.Errorf("the existing file was modified: %q", got)
	}
	// And none of the others was created, so there is no half-feature to
	// identify and remove by hand.
	for _, file := range plan.Files {
		if file.Path == collision {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file.Path))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was created despite the collision", file.Path)
		}
	}
}

// TestWriteCreatesEveryFileOnce covers the ordinary path.
func TestWriteCreatesEveryFileOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	plan, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	written, err := feature.Write(root, plan)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != len(plan.Files) {
		t.Fatalf("wrote %d files, want %d", len(written), len(plan.Files))
	}
	for _, file := range plan.Files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path))) // #nosec G304 -- a path this test just planned.
		if err != nil {
			t.Fatalf("reading %s: %v", file.Path, err)
		}
		if string(got) != string(file.Content) {
			t.Errorf("%s on disk differs from the plan", file.Path)
		}
	}

	// A second Write is refused, naming everything that is there.
	if _, err := feature.Write(root, plan); !errors.Is(err, feature.ErrExists) {
		t.Fatalf("second Write = %v, want feature.ErrExists", err)
	}
}

// TestModulePathOfReadsTheProjectRatherThanAsking keeps a generated import
// from disagreeing with the module it is generated into.
func TestModulePathOfReadsTheProjectRatherThanAsking(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := feature.ModulePathOf(root)
	if err != nil {
		t.Fatalf("ModulePathOf: %v", err)
	}
	if got != "example.com/demo" {
		t.Errorf("ModulePathOf = %q, want example.com/demo", got)
	}

	if _, err := feature.ModulePathOf(t.TempDir()); !errors.Is(err, feature.ErrNotAProject) {
		t.Errorf("ModulePathOf in an empty directory = %v, want feature.ErrNotAProject", err)
	}
}
