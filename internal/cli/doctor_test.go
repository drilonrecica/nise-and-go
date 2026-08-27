package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/doctor"
)

func TestCheckResultHumanRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    doctor.Check
		want string
	}{
		{
			name: "ok check with found and required, no remedy",
			c:    doctor.Check{Name: "go", Status: doctor.StatusOK, Found: "go 1.26.5", Required: "go 1.26.0 or newer"},
			want: "[OK] go\n    found:    go 1.26.5\n    required: go 1.26.0 or newer",
		},
		{
			name: "failing check includes remedy",
			c: doctor.Check{
				Name: "container-runtime", Status: doctor.StatusFail,
				Found: "neither docker nor podman responded", Required: "Docker or Podman",
				Remedy: "Install Docker or Podman.",
			},
			want: "[FAIL] container-runtime\n    found:    neither docker nor podman responded\n" +
				"    required: Docker or Podman\n    remedy:   Install Docker or Podman.",
		},
		{
			name: "skipped check with no remedy",
			c:    doctor.Check{Name: "recipe", Status: doctor.StatusSkipped, Found: "no nise.json found", Required: "present only inside a project"},
			want: "[SKIPPED] recipe\n    found:    no nise.json found\n    required: present only inside a project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkResult{tt.c}.Human()
			if got != tt.want {
				t.Errorf("Human() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// runDoctorFixture runs the real doctorCommand against the real
// environment (it does not inject a fake Runner: internal/doctor.Run
// exercises the actual `go`, `node`, `pnpm`, `docker`/`podman`, and
// `go tool` binaries on whatever machine runs this test). This is
// deliberate: docs/toolchain.md's own verified reference environment is
// exactly what this repository's tests run in, and internal/doctor already
// has exhaustive fake-Runner unit tests for the decision logic itself —
// this test instead proves the CLI wiring end to end: dispatch, per-check
// JSON documents, the human/JSON exit-code contract, and that no line
// this command prints is anything other than valid, parseable output.
func runDoctorFixture(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(args, IO{Stdout: &out, Stderr: &errOut, Getenv: emptyGetenv}, []*Command{doctorCommand()})
	return code, out.String(), errOut.String()
}

func TestDoctorHumanModeListsEveryCheck(t *testing.T) {
	code, stdout, stderr := runDoctorFixture(t, []string{"doctor"})

	wantNames := []string{"go", "node", "pnpm", "container-runtime", "sqlc", "goose", "oapi-codegen", "recipe", "recipe-compatibility"}
	for _, name := range wantNames {
		if !strings.Contains(stdout, "] "+name) {
			t.Errorf("stdout is missing check %q; stdout=%s", name, stdout)
		}
	}

	if code == int(clierr.ExitPrecondition) {
		if stderr == "" {
			t.Error("exit code is ExitPrecondition but stderr is empty; a failing doctor run must explain why")
		}
	} else if code != int(clierr.ExitOK) {
		t.Errorf("exit code = %d, want %d (ok) or %d (precondition failed); stdout=%s stderr=%s",
			code, clierr.ExitOK, clierr.ExitPrecondition, stdout, stderr)
	}
}

// jsonCheckLine is the subset of doctor.Check's JSON shape this test reads
// back, kept separate from doctor.Check itself so the test asserts against
// the wire shape, not against sharing a Go type with the production code.
type jsonCheckLine struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type jsonErrorLine struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// TestDoctorJSONModeIsValidNDJSONAndExitCodeMatchesContent proves --json
// output is exactly what docs/cli-output.md promises: one JSON document
// per line, every line parses, and the process exit code is 0 exactly
// when no check line reports "fail" (ExitPrecondition otherwise) — derived
// from the JSON content itself rather than assumed, so this test's
// expectations hold whether or not the machine running it happens to have
// every golden-profile tool installed.
func TestDoctorJSONModeIsValidNDJSONAndExitCodeMatchesContent(t *testing.T) {
	code, stdout, stderr := runDoctorFixture(t, []string{"doctor", "--json"})

	if stderr != "" {
		t.Errorf("stderr = %q, want empty: JSON mode keeps everything, errors included, on stdout", stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("stdout produced no lines")
	}

	seen := map[string]string{}
	var sawErrorLine bool
	for i, line := range lines {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\nline: %s", i, err, line)
		}
		if _, isError := probe["error"]; isError {
			sawErrorLine = true
			var e jsonErrorLine
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("line %d looked like an error envelope but did not decode: %v", i, err)
			}
			if e.Error.Code == "" || e.Error.Message == "" {
				t.Errorf("line %d: error envelope missing code or message: %+v", i, e)
			}
			continue
		}
		var c jsonCheckLine
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("line %d is not a valid check document: %v\nline: %s", i, err, line)
		}
		if c.Name == "" || c.Status == "" {
			t.Errorf("line %d: check document missing name or status: %+v", i, c)
		}
		seen[c.Name] = c.Status
	}

	wantNames := []string{"go", "node", "pnpm", "container-runtime", "sqlc", "goose", "oapi-codegen", "recipe", "recipe-compatibility"}
	for _, name := range wantNames {
		if _, ok := seen[name]; !ok {
			t.Errorf("no JSON line reported check %q", name)
		}
	}

	anyFail := false
	for _, status := range seen {
		if status == string(doctor.StatusFail) {
			anyFail = true
		}
	}

	if anyFail {
		if code != int(clierr.ExitPrecondition) {
			t.Errorf("a check reported \"fail\" but exit code = %d, want %d", code, clierr.ExitPrecondition)
		}
		if !sawErrorLine {
			t.Error("a check reported \"fail\" but no error document was emitted in JSON mode")
		}
	} else {
		if code != int(clierr.ExitOK) {
			t.Errorf("no check reported \"fail\" but exit code = %d, want %d", code, clierr.ExitOK)
		}
		if sawErrorLine {
			t.Error("no check failed but an error document was still emitted")
		}
	}
}
