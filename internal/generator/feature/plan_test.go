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

func resourceOptions() feature.Options {
	return feature.Options{
		Kind:          feature.KindResource,
		Name:          "order",
		ModulePath:    "example.com/demo",
		NextMigration: 9,
	}
}

// TestResourcePlanWritesTheDataLayerAndNotTheToolsOutput pins what a resource
// adds and, just as importantly, what it does not: sqlc's store/ is written by
// sqlc, and a generator that wrote a plausible copy of another tool's output
// would produce files that disagree with the tool the moment either changes.
func TestResourcePlanWritesTheDataLayerAndNotTheToolsOutput(t *testing.T) {
	t.Parallel()

	content := planContent(t, resourceOptions())

	for _, path := range []string{
		"db/migrations/00009_orders.sql",
		"internal/features/order/queries/order.sql",
		"internal/features/order/sqlc.yaml",
		"internal/features/order/usecase.go",
		"internal/features/order/order_test.go",
	} {
		if _, exists := content[path]; !exists {
			t.Errorf("the resource plan lacks %s", path)
		}
	}
	for path := range content {
		if strings.Contains(path, "/store/") {
			t.Errorf("the plan writes %s; store/ is sqlc's output, not nise's", path)
		}
	}
}

// TestResourceMigrationIsNumberedNotTimestamped keeps the history contiguous.
// The runtime's compatibility check refuses a gap, and a gap is how a missing
// migration hides; a timestamped filename would also reintroduce exactly the
// nondeterminism ADR 0002 forbids.
func TestResourceMigrationIsNumberedNotTimestamped(t *testing.T) {
	t.Parallel()

	content := planContent(t, resourceOptions())
	migration, exists := content["db/migrations/00009_orders.sql"]
	if !exists {
		t.Fatal("the migration is not numbered 00009")
	}
	for _, fragment := range []string{
		"-- +goose Up",
		"-- +goose Down",
		"CREATE TABLE orders (",
		// The bound is in the database as well as in Go: a check the database
		// enforces holds for a bulk import and a hand-written UPDATE too.
		"CHECK (length(name) BETWEEN 1 AND 200)",
		// The index matches the list's (created_at, id) order exactly; an
		// index in a different order is one the planner will not use.
		"CREATE INDEX orders_created_at_id_idx ON orders (created_at DESC, id DESC);",
	} {
		if !strings.Contains(migration, fragment) {
			t.Errorf("the migration lacks %q", fragment)
		}
	}

	// A resource with no migration number is refused rather than numbered
	// zero, or numbered from the clock.
	opts := resourceOptions()
	opts.NextMigration = 0
	if _, err := feature.NewPlan(opts); err == nil {
		t.Error("NewPlan accepted a resource with no migration version")
	}
}

// TestResourceQueriesCarryNoOwnershipHeader is ADR 0009's resolution for a
// file format whose comments are copied elsewhere: sqlc puts a query's leading
// comment into the generated Go doc comment, so a Nise header there would
// appear inside a tool-owned file and contradict that file's own marker.
func TestResourceQueriesCarryNoOwnershipHeader(t *testing.T) {
	t.Parallel()

	queries := planContent(t, resourceOptions())["internal/features/order/queries/order.sql"]
	if strings.Contains(queries, "Generated once by nise") {
		t.Error("the query file carries an ownership header; sqlc would copy it into its own output")
	}
	// The query names are the exported Go identifiers sqlc will emit, so the
	// plural has to be the title-cased one.
	if !strings.Contains(queries, "-- name: ListOrders :many") {
		t.Error("the list query is not named ListOrders")
	}
}

// TestResourcePlanNamesTheFollowUpCommands covers the other half of ADR 0026:
// generation runs no toolchain, so what has to run afterwards is printed.
func TestResourcePlanNamesTheFollowUpCommands(t *testing.T) {
	t.Parallel()

	plan, err := feature.NewPlan(resourceOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if len(plan.Commands) == 0 {
		t.Fatal("a resource plan names no follow-up commands; store/ would never be written")
	}
	if !strings.Contains(plan.Commands[0], "sqlc-generate") {
		t.Errorf("the first command is %q, want sqlc generation first", plan.Commands[0])
	}

	// A feature adds no table and no SQL, so it needs none of them.
	featurePlan, err := feature.NewPlan(featureOptions())
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if len(featurePlan.Commands) != 0 {
		t.Errorf("a feature plan names commands %v; it adds no SQL", featurePlan.Commands)
	}
}

// TestResourceContractIsAFragmentThatMerges pins how the OpenAPI reaches the
// authoritative document: as a fragment whose refs resolve after a merge, not
// as a second document generated code also reads.
//
// External `$ref` across files was the first shape tried and does not work:
// the pinned generator refuses a back-reference into the main document without
// an import mapping. A fragment written to merge is what remains, and it keeps
// api/openapi.yaml the single authoritative contract.
func TestResourceContractIsAFragmentThatMerges(t *testing.T) {
	t.Parallel()

	fragment, exists := planContent(t, resourceOptions())["api/order.yaml"]
	if !exists {
		t.Fatal("the resource plan has no OpenAPI fragment")
	}
	// Every reference is local, so it resolves once merged.
	if strings.Contains(fragment, "openapi.yaml#") {
		t.Error("the fragment refers back to openapi.yaml; that does not resolve for the pinned generator")
	}
	for _, fragmentText := range []string{
		"operationId: listOrders",
		"operationId: createOrder",
		"operationId: getOrder",
		"operationId: updateOrder",
		"operationId: deleteOrder",
		// The domain-command example: a verb with its own permission and its
		// own meaning, rather than a nullable field on the record.
		"operationId: archiveOrder",
		// The input is a separate schema from the resource: the identifier and
		// the timestamps are the server's.
		"OrderInput:",
		"additionalProperties: false",
	} {
		if !strings.Contains(fragment, fragmentText) {
			t.Errorf("the fragment lacks %q", fragmentText)
		}
	}
}

// TestResourceHandlerDelegatesAndDecidesNothing pins the transport rule: the
// handler translates, and every decision belongs to the use case.
func TestResourceHandlerDelegatesAndDecidesNothing(t *testing.T) {
	t.Parallel()

	handler, exists := planContent(t, resourceOptions())["internal/platform/httpapi/order.go"]
	if !exists {
		t.Fatal("the resource plan has no handler")
	}
	for _, fragment := range []string{
		"func (s *Server) ListOrders(",
		"func (s *Server) ArchiveOrder(",
		// Every operation checks for a session before it reaches a use case.
		"if _, err := requireSession(ctx); err != nil {",
		// The cursor's binding fingerprints this operation's own filters, so
		// changing a filter invalidates a cursor rather than paging into a
		// different result set.
		"pagination.NewBinding(orderResource, filters)",
		// A collection is never null.
		"items := make([]openapigen.Order, 0, len(page.Items))",
	} {
		if !strings.Contains(handler, fragment) {
			t.Errorf("the handler lacks %q", fragment)
		}
	}
	// No authorization decision in the transport: the use case owns it.
	if strings.Contains(handler, "authorization.Require(") {
		t.Error("the handler checks a permission itself; that belongs in the use case, where every caller reaches it")
	}
}

// TestResourceFrontendIsACompleteFlow pins the four pages and the one form
// component they share: create and edit differ in what they start with and
// where they go afterwards, and a second copy of the form would be a second
// place to add a field.
func TestResourceFrontendIsACompleteFlow(t *testing.T) {
	t.Parallel()

	content := planContent(t, resourceOptions())

	for _, path := range []string{
		"frontend/src/lib/features/order/api.ts",
		"frontend/src/lib/features/order/OrderForm.svelte",
		"frontend/src/routes/(app)/orders/+page.svelte",
		"frontend/src/routes/(app)/orders/new/+page.svelte",
		"frontend/src/routes/(app)/orders/[id]/+page.svelte",
		"frontend/src/routes/(app)/orders/[id]/edit/+page.svelte",
	} {
		if _, exists := content[path]; !exists {
			t.Errorf("the resource plan lacks %s", path)
		}
	}

	// Every call goes through the application's one client, which supplies the
	// versioned base, the anti-forgery header, and cancellation.
	api := content["frontend/src/lib/features/order/api.ts"]
	if !strings.Contains(api, "import { api } from '$lib/api/client';") {
		t.Error("the feature's api.ts does not use the application's client")
	}
	for _, page := range []string{
		"frontend/src/routes/(app)/orders/+page.svelte",
		"frontend/src/routes/(app)/orders/new/+page.svelte",
		"frontend/src/routes/(app)/orders/[id]/+page.svelte",
		"frontend/src/routes/(app)/orders/[id]/edit/+page.svelte",
	} {
		if strings.Contains(content[page], "fetch(") {
			t.Errorf("%s calls fetch directly", page)
		}
	}

	// The list renders all four states, not only the one with rows.
	list := content["frontend/src/routes/(app)/orders/+page.svelte"]
	for _, fragment := range []string{"<Skeleton", "<EmptyState", "<ProblemAlert", "<Table"} {
		if !strings.Contains(list, fragment) {
			t.Errorf("the list page lacks %s", fragment)
		}
	}

	// The browser schema mirrors the server's bound rather than exceeding it: a
	// stricter rule here refuses input the server would accept.
	form := content["frontend/src/lib/features/order/OrderForm.svelte"]
	if !strings.Contains(form, "v.maxLength(200,") {
		t.Error("the form's schema does not mirror the server's 200-character bound")
	}
}

// TestGeneratedListPutsItsStateInTheURL pins the list contract (Nise task
// M7-006): search and the page somebody is on live in the query string,
// changing the search drops the cursor, and the page reloads when the URL
// changes so the back button works.
func TestGeneratedListPutsItsStateInTheURL(t *testing.T) {
	t.Parallel()

	list := planContent(t, resourceOptions())["frontend/src/routes/(app)/orders/+page.svelte"]
	for _, fragment := range []string{
		"import { listHref, listQuery, readListState } from '$lib/table/list-state';",
		"readListState(page.url, filters)",
		"listQuery(listState)",
		// Paging is navigation: real hrefs, in history, openable in a new tab.
		"listHref(page.url, { after: result.page.next_cursor }, filters)",
		"listHref(page.url, { before: result.page.prev_cursor }, filters)",
		// Reloading on every URL change is what makes the back button work.
		"const controller = new AbortController();",
		"return () => controller.abort();",
		// A superseded request is not a failure to report.
		"error.name === 'AbortError'",
		// Searching replaces the history entry rather than adding one per
		// keystroke somebody submitted.
		"{ replaceState: true, keepFocus: true }",
	} {
		if !strings.Contains(list, fragment) {
			t.Errorf("the list page lacks %q", fragment)
		}
	}

	// Sorting is deliberately not wired, and the file has to say why: a cursor
	// encodes a position in a particular order, so a sortable column has to
	// join the cursor's key and the query's tuple comparison, or a sorted page
	// and its cursor disagree about the boundary.
	if !strings.Contains(list, "Sorting is deliberately not wired yet") {
		t.Error("the list page does not explain why sorting needs a decision before it is added")
	}

	// The empty state distinguishes "nothing yet" from "nothing matched",
	// which are two different things to do next.
	if !strings.Contains(list, "listState.search ?") {
		t.Error("the list's empty state does not distinguish an empty collection from an empty search")
	}
}

// TestGeneratedActionsArePermissionAwareAndConfirmDestruction pins the two
// halves of M7-007: an action the session cannot perform is not offered, and
// one that destroys something asks first.
func TestGeneratedActionsArePermissionAwareAndConfirmDestruction(t *testing.T) {
	t.Parallel()

	content := planContent(t, resourceOptions())

	for _, path := range []string{
		"frontend/src/routes/(app)/orders/+page.svelte",
		"frontend/src/routes/(app)/orders/[id]/+page.svelte",
	} {
		body := content[path]
		if !strings.Contains(body, "session.can('orders.manage')") {
			t.Errorf("%s does not gate its actions on a permission", path)
		}
		// It has to say what it is and is not, or the next reader treats a
		// hidden button as a security control.
		if !strings.Contains(body, "courtesy") {
			t.Errorf("%s does not say that hiding an action is a courtesy rather than a control", path)
		}
	}

	detail := content["frontend/src/routes/(app)/orders/[id]/+page.svelte"]
	for _, fragment := range []string{
		"<ConfirmDialog",
		"destructive",
		// The question says what is lost. "Are you sure" tells somebody
		// nothing they did not already believe.
		"This removes the order permanently.",
	} {
		if !strings.Contains(detail, fragment) {
			t.Errorf("the detail page lacks %q", fragment)
		}
	}
}
