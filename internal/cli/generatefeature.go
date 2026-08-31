package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator/feature"
)

// featureGenerator is the Generator `nise generate` wires in: it plans a
// slice, refuses to write over anything, and writes the rest.
//
// It edits no file it did not create. Everything a slice needs in a file the
// application owns is carried back in the plan and printed, never applied
// (ADR 0026).
type featureGenerator struct{}

func (featureGenerator) GenerateFeature(ctx context.Context, root, name string) (feature.Plan, error) {
	return generateSlice(ctx, root, name, feature.KindFeature)
}

func (featureGenerator) GenerateResource(ctx context.Context, root, name string) (feature.Plan, error) {
	return generateSlice(ctx, root, name, feature.KindResource)
}

// generateSlice plans, then writes. The plan is built entirely in memory
// first, so a template that fails to render fails before a single file exists.
func generateSlice(_ context.Context, root, name string, kind feature.Kind) (feature.Plan, error) {
	modulePath, err := feature.ModulePathOf(root)
	if err != nil {
		return feature.Plan{}, err
	}
	plan, err := feature.NewPlan(feature.Options{Kind: kind, Name: name, ModulePath: modulePath})
	if err != nil {
		return feature.Plan{}, err
	}
	if _, err := feature.Write(root, plan); err != nil {
		return feature.Plan{}, err
	}
	return plan, nil
}

// generateResult is what the command reports.
//
// The insertions are the substance of it, not a footnote: they are the part
// the person has to do, and a command that buried them under a list of
// filenames would be a command whose output nobody finished reading.
type generateResult struct {
	Kind string       `json:"kind"`
	Name string       `json:"name"`
	Plan feature.Plan `json:"plan"`
}

// Human renders the result for a terminal.
func (r generateResult) Human() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Created %s %q — %d files\n", r.Kind, r.Name, len(r.Plan.Files))
	for _, file := range r.Plan.Files {
		fmt.Fprintf(&out, "  %s\n", file.Path)
	}

	if len(r.Plan.Insertions) == 0 {
		return out.String()
	}

	out.WriteString("\nNow add these, in this order. nise does not edit files it did not\n")
	out.WriteString("write, so every change to a file you own stays in your own diff.\n\n")
	for _, insertion := range r.Plan.Insertions {
		out.WriteString(insertion.String())
		out.WriteString("\n")
	}
	return out.String()
}
