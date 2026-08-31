package generator_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestGeneratedProjectDefinesPaginationContracts(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	wants := map[string][]string{
		"api/openapi.yaml": {
			"CursorPage:",
			"ReportPage:",
			"APIRootReportCollection:",
			"    ReportPageNumber:",
			"    ReportPageSize:",
			"APIRootCursorCollection:",
			"    Cursor:",
			"    CursorLimit:",
			"    CursorAfter:",
			"    CursorBefore:",
			"$ref: '#/components/schemas/CursorPage'",
		},
		"internal/platform/httpapi/openapigen/openapi.gen.go": {
			"type CursorPage struct",
			"type ReportPage struct",
			"type APIRootReportCollection struct",
			"type APIRootCursorCollection struct",
			"type Cursor = string",
			"NextCursor *Cursor `json:\"next_cursor,omitempty\"`",
			"PrevCursor *Cursor `json:\"prev_cursor,omitempty\"`",
		},
		"internal/platform/httpapi/api.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/pagination"`,
			"func NewServer(cursors *pagination.Codec) (*Server, error)",
			"func cursorBinding(r *http.Request, resource string) pagination.Binding",
			"case pagination.LimitParam, pagination.AfterParam, pagination.BeforeParam:",
			"func issueCursorPage(",
			"func newAPIRootCursorCollection(",
			"func issueReportPage(totals pagination.Totals) openapigen.ReportPage",
			"func newAPIRootReportCollection(",
			"errors.Is(err, pagination.ErrReportTooDeep)",
			"items = []openapigen.APIRoot{}",
			"func paginationProblem(err error) problem.Definition",
			"errors.Is(err, pagination.ErrCursorExpired)",
		},
		"internal/platform/httpapi/problem/problem.go": {
			`"invalid_pagination"`,
			`"cursor_expired"`,
			`"report_too_deep"`,
			"func InvalidPagination() Definition",
			"func CursorExpired() Definition",
			"func ReportTooDeep() Definition",
		},
		"internal/platform/httpapi/api_test.go": {
			"TestCursorCollectionResponseShape",
			"TestCursorBindingIgnoresOnlyThePaginationParameters",
			"TestIssueCursorPage",
			"TestPaginationProblemMapsEveryFailureToTheClient",
			"TestNewServerRequiresACursorCodec",
			"TestReportCollectionResponseShape",
			"TestReportPageEchoesTheRequestedPage",
			"TestReportRefusesDepthAndMixedPagination",
		},
		"internal/platform/config/config.go": {
			"CursorKeys *pagination.KeyRing",
			"CursorKeysAreEphemeral bool",
			"CursorTTL time.Duration",
			`l.Secret("CURSOR_SIGNING_KEY"`,
			`l.Secret("CURSOR_RETIRED_KEYS"`,
			`l.Duration("CURSOR_TTL"`,
			"maxCursorTTL = 24 * time.Hour",
			`v.Check(!cfg.CursorKeysAreEphemeral, "CURSOR_SIGNING_KEY"`,
		},
		"internal/platform/config/config_test.go": {
			"TestLoadBuildsTheCursorKeyRing",
			"TestLoadGeneratesAnEphemeralCursorKeyOutsideProduction",
			"TestProductionRefusesAnEphemeralCursorKey",
			"TestLoadRejectsUnusableCursorConfiguration",
		},
		"internal/app/app.go": {
			"pagination.NewCodec(cfg.CursorKeys, cfg.CursorTTL)",
			"httpapi.NewServer(cursors)",
		},
		".env.example": {
			"CURSOR_SIGNING_KEY=",
			"CURSOR_RETIRED_KEYS=",
			"CURSOR_TTL=1h",
		},
		"frontend/src/lib/api/schema.d.ts": {
			"CursorPage:",
			"ReportPage:",
			"APIRootCursorCollection:",
			"APIRootReportCollection:",
			"Cursor: string;",
			`next_cursor?: components["schemas"]["Cursor"];`,
			`prev_cursor?: components["schemas"]["Cursor"];`,
		},
		"frontend/src/lib/api/client.test.ts": {
			"types cursor pages as opaque tokens that are absent, never null",
			"an absent cursor is omitted, never null",
			"types report pages as a separate page/size/total contract",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The neutral Page must not acquire cursor members: the two contracts
	// exist separately so a reporting page and a cursor page cannot be
	// confused for one another.
	document, err := openapi3.NewLoader().LoadFromData([]byte(content["api/openapi.yaml"]))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI: %v", err)
	}
	requireClosedObjectSchema(t, document, "Page", "has_more")

	cursorPage := requireOpenObjectSchema(t, document,
		"CursorPage",
		[]string{"has_more"},
		[]string{"has_more", "next_cursor", "prev_cursor"},
	)
	requirePropertyType(t, cursorPage, "has_more", "boolean")
	for _, member := range []string{"next_cursor", "prev_cursor"} {
		property := cursorPage.Properties[member]
		if property == nil || property.Ref != "#/components/schemas/Cursor" {
			t.Errorf("CursorPage.%s ref = %#v, want Cursor", member, property)
		}
		if property != nil && property.Value != nil && property.Value.Nullable {
			t.Errorf("CursorPage.%s is nullable; an absent page has no member at all", member)
		}
	}

	cursor := document.Components.Schemas["Cursor"]
	if cursor == nil || cursor.Value == nil {
		t.Fatal("OpenAPI lacks the Cursor schema")
	}
	if !cursor.Value.Type.Is("string") {
		t.Errorf("Cursor type = %v, want string", cursor.Value.Type)
	}
	if cursor.Value.MaxLength == nil || *cursor.Value.MaxLength != 4096 || cursor.Value.MinLength != 1 {
		t.Errorf("Cursor bounds = %v..%v, want 1..4096", cursor.Value.MinLength, cursor.Value.MaxLength)
	}

	collection := requireClosedObjectSchema(t, document, "APIRootCursorCollection", "items", "page")
	items := requirePropertyType(t, collection, "items", "array")
	if items.Items == nil || items.Items.Ref != "#/components/schemas/APIRoot" {
		t.Errorf("APIRootCursorCollection.items element ref = %#v, want APIRoot", items.Items)
	}
	if page := collection.Properties["page"]; page == nil || page.Ref != "#/components/schemas/CursorPage" {
		t.Errorf("APIRootCursorCollection.page ref = %#v, want CursorPage", page)
	}

	reportPage := requireClosedObjectSchema(t, document, "ReportPage",
		"page", "size", "total", "total_pages", "has_more")
	requirePropertyType(t, reportPage, "has_more", "boolean")
	for property, format := range map[string]string{
		"page": "int32", "size": "int32", "total": "int64", "total_pages": "int64",
	} {
		schema := requirePropertyType(t, reportPage, property, "integer")
		if schema.Format != format {
			t.Errorf("ReportPage.%s format = %q, want %q", property, schema.Format, format)
		}
		if schema.Min == nil {
			t.Errorf("ReportPage.%s has no minimum", property)
		}
	}
	// The two page contracts must stay disjoint: neither may quietly grow the
	// other's members and become interchangeable at a call site.
	for member := range reportPage.Properties {
		if _, shared := cursorPage.Properties[member]; shared && member != "has_more" {
			t.Errorf("ReportPage and CursorPage share member %q", member)
		}
	}

	reportCollection := requireClosedObjectSchema(t, document, "APIRootReportCollection", "items", "page")
	if page := reportCollection.Properties["page"]; page == nil || page.Ref != "#/components/schemas/ReportPage" {
		t.Errorf("APIRootReportCollection.page ref = %#v, want ReportPage", page)
	}

	wantParameters := map[string]string{
		"CursorLimit":      "limit",
		"CursorAfter":      "after",
		"CursorBefore":     "before",
		"ReportPageNumber": "page",
		"ReportPageSize":   "size",
	}
	for component, name := range wantParameters {
		parameter := document.Components.Parameters[component]
		if parameter == nil || parameter.Value == nil {
			t.Errorf("OpenAPI lacks the %s parameter component", component)
			continue
		}
		if parameter.Value.Name != name || parameter.Value.In != openapi3.ParameterInQuery {
			t.Errorf("%s = %q in %q, want %q in query", component, parameter.Value.Name, parameter.Value.In, name)
		}
		if parameter.Value.Required {
			t.Errorf("%s is required; pagination parameters are optional", component)
		}
	}
	limit := document.Components.Parameters["CursorLimit"]
	if limit != nil && limit.Value != nil && limit.Value.Schema != nil && limit.Value.Schema.Value != nil {
		schema := limit.Value.Schema.Value
		if schema.Min == nil || *schema.Min != 1 || schema.Max == nil || *schema.Max != 200 {
			t.Errorf("CursorLimit bounds = %v..%v, want 1..200", schema.Min, schema.Max)
		}
	}
}

// requireOpenObjectSchema asserts a closed-property object whose required set
// is a strict subset of its properties. It is the shape a page contract needs:
// no additional properties, an exact property list, and optional members that
// are genuinely optional rather than nullable.
func requireOpenObjectSchema(t *testing.T, document *openapi3.T, name string, required, properties []string) *openapi3.Schema {
	t.Helper()

	ref := document.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("OpenAPI lacks schema %s", name)
	}
	schema := ref.Value
	if !schema.Type.Is("object") {
		t.Errorf("%s type = %v, want object", name, schema.Type)
	}
	if schema.Nullable {
		t.Errorf("%s is nullable", name)
	}
	if schema.AdditionalProperties.Has == nil || *schema.AdditionalProperties.Has {
		t.Errorf("%s additionalProperties = %#v, want false", name, schema.AdditionalProperties)
	}
	gotRequired := slices.Clone(schema.Required)
	slices.Sort(gotRequired)
	wantRequired := slices.Clone(required)
	slices.Sort(wantRequired)
	if !slices.Equal(gotRequired, wantRequired) {
		t.Errorf("%s required = %v, want exactly %v", name, gotRequired, wantRequired)
	}
	gotProperties := slices.Sorted(maps.Keys(schema.Properties))
	wantProperties := slices.Clone(properties)
	slices.Sort(wantProperties)
	if !slices.Equal(gotProperties, wantProperties) {
		t.Errorf("%s properties = %v, want exactly %v", name, gotProperties, wantProperties)
	}
	return schema
}
