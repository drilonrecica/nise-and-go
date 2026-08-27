package observability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGaugeFuncVecStaysUnexported guards the fix for a review finding
// (task-9-report.md, fix round 1, finding 1): a pull-sampled gauge (the
// type backing PoolMetrics) has no cardinality cap, unlike CounterVec,
// GaugeVec, and HistogramVec, because its series are meant to be
// registered once per pool at startup, not created from request-controlled
// input. Exporting that type, its constructor, or its registration method
// would hand an application an uncapped path to unbounded cardinality with
// nothing in the type system to stop a request handler from calling it.
//
// This is a static, source-level check rather than a compile failure test
// (Go has no supported way to assert "this identifier does not exist and
// is not exported" from within the same package at runtime) so that a
// future change re-exporting any of these three identifiers fails a test
// immediately, rather than silently reopening the cardinality gap the fix
// closed.
func TestGaugeFuncVecStaysUnexported(t *testing.T) {
	forbidden := map[string]bool{
		"GaugeFuncVec":    true, // the type itself
		"NewGaugeFuncVec": true, // its constructor, as a Registry method
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.TypeSpec:
				if forbidden[decl.Name.Name] {
					t.Errorf("%s:%s exports type %q; it must stay unexported (see gauge.go's gaugeFuncVec doc comment)",
						name, fset.Position(decl.Pos()), decl.Name.Name)
				}
			case *ast.FuncDecl:
				if forbidden[decl.Name.Name] {
					t.Errorf("%s:%s exports func %q; it must stay unexported (see registry.go's newGaugeFuncVec doc comment)",
						name, fset.Position(decl.Pos()), decl.Name.Name)
				}
				// An exported "Add" method on the unexported gaugeFuncVec
				// receiver would be just as bad as exporting the type: an
				// application could still reach it through an interface
				// or a returned value. There is no such method today
				// (the method is named "add"), but check the shape
				// directly rather than relying only on the name check
				// above never regressing.
				if decl.Name.IsExported() && decl.Recv != nil {
					for _, field := range decl.Recv.List {
						if receiverTypeName(field.Type) == "gaugeFuncVec" {
							t.Errorf("%s:%s exports method %q on gaugeFuncVec; every method on this type must stay unexported",
								name, fset.Position(decl.Pos()), decl.Name.Name)
						}
					}
				}
			}
			return true
		})
	}
}

// receiverTypeName extracts the bare type name from a method receiver
// expression, unwrapping a leading pointer if present.
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}
