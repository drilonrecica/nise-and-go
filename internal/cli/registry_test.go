package cli

import (
	"strings"
	"testing"
)

// TestCommandsHaveNoReservedFlagCollisions walks the real production
// registry — every entry Commands() returns, at every nesting depth — and
// fails the build if any command defines its own flag that collides with
// a reserved global flag name (see reservedFlagNames in dispatch.go).
//
// This is the second, static half of the collision defense the runtime
// check in Execute provides: it catches the mistake the moment a future
// task adds a broken command, in `go test`, before anyone has to actually
// run that command and discover the bug from its behavior (silent
// shadowing with exit 0 — see dispatch_test.go's
// TestExecuteReservedFlagCollisionIsALoudFailure for what that looks
// like).
func TestCommandsHaveNoReservedFlagCollisions(t *testing.T) {
	t.Parallel()
	checkNoReservedFlagCollisions(t, nil, Commands())
}

func checkNoReservedFlagCollisions(t *testing.T, path []string, cmds []*Command) {
	t.Helper()
	for _, cmd := range cmds {
		cmdPath := append(append([]string{}, path...), cmd.Name)
		if collisions := reservedFlagCollisions(newCommandFlagSet(cmd)); len(collisions) > 0 {
			t.Errorf("command %q defines flag(s) colliding with a reserved global flag: %v",
				"nise "+strings.Join(cmdPath, " "), collisions)
		}
		if len(cmd.Subcommands) > 0 {
			checkNoReservedFlagCollisions(t, cmdPath, cmd.Subcommands)
		}
	}
}
