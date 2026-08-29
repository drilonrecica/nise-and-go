package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
)

func TestDatabaseCommandsAreRegistered(t *testing.T) {
	t.Parallel()
	db := findByName(Commands(), "db")
	if db == nil {
		t.Fatal("Commands() does not include db")
	}
	for _, name := range []string{"migrate", "status"} {
		if findByName(db.Subcommands, name) == nil {
			t.Errorf("db command does not include %s", name)
		}
	}
}

func TestDatabaseCommandOutsideProjectFailsAsPrecondition(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	code, stdout, stderr := runDatabaseFixture([]string{"db", "status", "--json"})
	if code != int(clierr.ExitPrecondition) {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, clierr.ExitPrecondition, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("JSON stderr = %q, want empty", stderr)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if envelope.Error.Code != "db.no_project" {
		t.Errorf("error code = %q, want db.no_project", envelope.Error.Code)
	}
}

func TestDatabaseCommandRejectsTrailingArguments(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	code, _, _ := runDatabaseFixture([]string{"db", "migrate", "unexpected"})
	if code != int(clierr.ExitUsage) {
		t.Fatalf("exit code = %d, want %d", code, clierr.ExitUsage)
	}
}

func runDatabaseFixture(args []string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, Commands())
	return code, out.String(), errOut.String()
}

func TestDecodeDatabaseCommandResult(t *testing.T) {
	t.Parallel()

	valid := `{"action":"status","state":"behind","current":1,"target":3,"pending":2}`
	got, err := decodeDatabaseCommandResult([]byte(valid), "status")
	if err != nil {
		t.Fatalf("decodeDatabaseCommandResult: %v", err)
	}
	if got.Current != 1 || got.Target != 3 || got.Pending != 2 {
		t.Errorf("result = %+v", got)
	}

	for name, tc := range map[string]struct {
		action string
		input  string
	}{
		"wrong action":          {action: "status", input: `{"action":"migrate","state":"current","current":1,"target":1,"pending":0}`},
		"unknown state":         {action: "status", input: `{"action":"status","state":"dirty","current":1,"target":1,"pending":0}`},
		"inconsistent pending":  {action: "status", input: `{"action":"status","state":"behind","current":1,"target":3,"pending":1}`},
		"uninitialized nonzero": {action: "status", input: `{"action":"status","state":"uninitialized","current":1,"target":3,"pending":2}`},
		"behind at target":      {action: "status", input: `{"action":"status","state":"behind","current":3,"target":3,"pending":0}`},
		"current below target":  {action: "status", input: `{"action":"status","state":"current","current":2,"target":3,"pending":1}`},
		"unknown field":         {action: "status", input: `{"action":"status","state":"current","current":1,"target":1,"pending":0,"secret":"x"}`},
		"trailing document":     {action: "status", input: `{"action":"status","state":"current","current":1,"target":1,"pending":0}{}`},
		"status applied list":   {action: "status", input: `{"action":"status","state":"current","current":1,"target":1,"pending":0,"applied":[1]}`},
		"unordered applied":     {action: "migrate", input: `{"action":"migrate","state":"current","current":2,"target":2,"pending":0,"applied":[2,1]}`},
		"gapped applied":        {action: "migrate", input: `{"action":"migrate","state":"current","current":3,"target":3,"pending":0,"applied":[1,3]}`},
		"applied below current": {action: "migrate", input: `{"action":"migrate","state":"current","current":3,"target":3,"pending":0,"applied":[1,2]}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeDatabaseCommandResult([]byte(tc.input), tc.action); err == nil {
				t.Fatal("invalid result was accepted")
			}
		})
	}
}

func TestDatabaseResultHumanDoesNotExposeAppliedVersions(t *testing.T) {
	t.Parallel()
	result := databaseCommandResult{Action: "migrate", State: "current", Current: 2, Target: 2, Applied: []int64{1, 2}}
	got := result.Human()
	if strings.Contains(got, "[1 2]") || !strings.Contains(got, "applied=2") {
		t.Errorf("Human() = %q", got)
	}
}

func TestDatabaseCommandEnvironmentReplacesToolchainSetting(t *testing.T) {
	t.Parallel()
	got := overlayEnvironment(
		[]string{"PATH=/bin", "GOTOOLCHAIN=auto", "GOTOOLCHAIN=go1.99"},
		map[string]string{"GOTOOLCHAIN": "local"},
	)
	if strings.Join(got, "\n") != "PATH=/bin\nGOTOOLCHAIN=local" {
		t.Fatalf("overlayEnvironment() = %q", got)
	}
}

func TestBoundedBufferRejectsOversizedApplicationOutput(t *testing.T) {
	t.Parallel()
	var got boundedBuffer
	got.limit = 4
	if n, err := got.Write([]byte("abcd")); n != 4 || err != nil {
		t.Fatalf("first Write() = (%d, %v), want (4, nil)", n, err)
	}
	if n, err := got.Write([]byte("e")); n != 0 || !errors.Is(err, errOutputLimit) {
		t.Fatalf("overflow Write() = (%d, %v), want (0, errOutputLimit)", n, err)
	}
}
