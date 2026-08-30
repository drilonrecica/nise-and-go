package generator_test

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestGeneratedProjectDefinesDirectAndCollectionResponseShapes(t *testing.T) {
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
			`$ref: '#/components/schemas/APIRoot'`,
			"APIRootCollection:",
			"Page:",
		},
		"internal/platform/httpapi/openapigen/openapi.gen.go": {
			"type APIRoot struct",
			"type APIRootCollection struct",
			"Items []APIRoot",
			"Page Page",
			"type GetAPIIndex200JSONResponse APIRoot",
		},
		"internal/platform/httpapi/api.go": {
			"openapigen.GetAPIIndex200JSONResponse",
			"func newAPIRootCollection(",
			"items = []openapigen.APIRoot{}",
		},
		"internal/platform/httpapi/api_test.go": {
			`wantBody := map[string]any{"version": "v1"}`,
			"TestCollectionResponseShape",
			"newAPIRootCollection(\n\t\tnil,",
		},
		"frontend/src/lib/api/schema.d.ts": {
			"APIRoot:",
			"APIRootCollection:",
			`items: components["schemas"]["APIRoot"][]`,
			`page: components["schemas"]["Page"]`,
		},
		"frontend/src/lib/api/client.test.ts": {
			"returns the direct resource without an envelope",
			`components['schemas']['APIRootCollection']`,
			"collection items are required and never null",
			"generic envelope members are not in the collection contract",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	document, err := openapi3.NewLoader().LoadFromData([]byte(content["api/openapi.yaml"]))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI: %v", err)
	}

	apiRoot := requireClosedObjectSchema(t, document, "APIRoot", "version")
	requirePropertyType(t, apiRoot, "version", "string")
	if got := apiRoot.Properties["version"].Value.Enum; len(got) != 1 || got[0] != "v1" {
		t.Errorf("APIRoot.version enum = %#v, want exactly [v1]", got)
	}

	page := requireClosedObjectSchema(t, document, "Page", "has_more")
	requirePropertyType(t, page, "has_more", "boolean")

	collection := requireClosedObjectSchema(t, document, "APIRootCollection", "items", "page")
	items := requirePropertyType(t, collection, "items", "array")
	if items.Items == nil || items.Items.Ref != "#/components/schemas/APIRoot" {
		t.Errorf("APIRootCollection.items element ref = %#v, want APIRoot", items.Items)
	}
	pageProperty := collection.Properties["page"]
	if pageProperty == nil || pageProperty.Ref != "#/components/schemas/Page" {
		t.Errorf("APIRootCollection.page ref = %#v, want Page", pageProperty)
	}

	path := document.Paths.Find("/")
	if path == nil || path.Get == nil {
		t.Fatal("OpenAPI lacks GET /")
	}
	response := path.Get.Responses.Status(200)
	if response == nil || response.Value == nil || response.Value.Content["application/json"] == nil {
		t.Fatal("GET / lacks a 200 application/json response")
	}
	if got := response.Value.Content["application/json"].Schema.Ref; got != "#/components/schemas/APIRoot" {
		t.Errorf("GET / response ref = %q, want APIRoot", got)
	}
}

func requireClosedObjectSchema(t *testing.T, document *openapi3.T, name string, properties ...string) *openapi3.Schema {
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
	want := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		want[property] = struct{}{}
	}
	gotRequired := make(map[string]struct{}, len(schema.Required))
	for _, property := range schema.Required {
		gotRequired[property] = struct{}{}
	}
	if !maps.Equal(gotRequired, want) {
		t.Errorf("%s required = %v, want exactly %v", name, schema.Required, properties)
	}
	gotProperties := make(map[string]struct{}, len(schema.Properties))
	for property := range schema.Properties {
		gotProperties[property] = struct{}{}
	}
	if !maps.Equal(gotProperties, want) {
		t.Errorf("%s properties = %v, want exactly %v", name, maps.Keys(gotProperties), properties)
	}
	return schema
}

func requirePropertyType(t *testing.T, schema *openapi3.Schema, property, wantType string) *openapi3.Schema {
	t.Helper()

	ref := schema.Properties[property]
	if ref == nil || ref.Value == nil {
		t.Fatalf("schema lacks resolved property %s", property)
	}
	if ref.Value.Nullable {
		t.Errorf("property %s is nullable", property)
	}
	if !ref.Value.Type.Is(wantType) {
		t.Errorf("property %s type = %v, want %s", property, ref.Value.Type, wantType)
	}
	return ref.Value
}
