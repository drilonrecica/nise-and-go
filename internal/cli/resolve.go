package cli

// resolveResult is the outcome of walking a command tree against a slice of
// arguments.
type resolveResult struct {
	ok bool

	// On success (ok == true):
	leaf      *Command // the resolved command
	path      []string // the name at each level leading to leaf, e.g. ["db", "migrate"]
	remaining []string // args left over for leaf's own flag.FlagSet

	// On failure (ok == false):
	parentPath []string   // path to the command group the lookup failed within (empty at top level)
	attempted  string     // the token that did not match anything
	searchedIn []*Command // the sibling set attempted was compared against, for suggestions
}

// resolveCommand walks cmds by matching args[0], args[1], ... against each
// level's command names, descending into Subcommands as long as names keep
// matching. It stops descending at the first token that does not match a
// subcommand name, or when the matched command has no Subcommands.
//
// A matched command with Subcommands but no direct Run (a pure group, like
// the planned "nise db") requires the next token to name one of its
// Subcommands: if there is no next token, resolution still succeeds (the
// caller shows that group's help); if there is a next token and it does not
// match, resolution fails with searchedIn set to that group's Subcommands
// so the suggestion is scoped to siblings, not the whole top-level list.
func resolveCommand(cmds []*Command, args []string) resolveResult {
	cur := cmds
	var path []string
	var leaf *Command
	i := 0

	for i < len(args) {
		next := findByName(cur, args[i])
		if next == nil {
			break
		}
		leaf = next
		path = append(path, args[i])
		i++
		cur = next.Subcommands
		if len(cur) == 0 {
			break
		}
	}

	if leaf == nil {
		return resolveResult{ok: false, attempted: args[0], searchedIn: cmds}
	}

	remaining := args[i:]
	if len(leaf.Subcommands) > 0 && leaf.Run == nil && len(remaining) > 0 {
		return resolveResult{
			ok:         false,
			parentPath: path,
			attempted:  remaining[0],
			searchedIn: leaf.Subcommands,
		}
	}

	return resolveResult{ok: true, leaf: leaf, path: path, remaining: remaining}
}

func findByName(cmds []*Command, name string) *Command {
	for _, c := range cmds {
		if c.Name == name {
			return c
		}
	}
	return nil
}
