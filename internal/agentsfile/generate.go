package agentsfile

import (
	"fmt"

	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

// Output is the deterministic pair of file contents Generate produces,
// ready to be written verbatim to disk.
type Output struct {
	// AgentsMD is the full content of AGENTS.md, header and all.
	AgentsMD []byte
	// Architecture is the full content of .nise/architecture.json, header
	// field and all.
	Architecture []byte
}

// Generate renders AGENTS.md and .nise/architecture.json for r. Calling it
// twice with an equal r always returns a byte-identical Output — see this
// package's doc comment for why.
func Generate(r recipe.Recipe) (Output, error) {
	agentsMD := wrapMarkdown(buildAgentsMDBody(r))

	archBytes, err := wrapArchitecture(buildArchitecture(r))
	if err != nil {
		return Output{}, fmt.Errorf("encode architecture map: %w", err)
	}

	return Output{AgentsMD: agentsMD, Architecture: archBytes}, nil
}
