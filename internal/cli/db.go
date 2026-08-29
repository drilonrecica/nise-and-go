package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/cli/clierr"
	"github.com/drilonrecica/nise-and-go/internal/recipe"
)

const dbDocs = "docs/commands/db.md"

const maxDatabaseCommandOutput = 64 << 10

var errOutputLimit = errors.New("application output exceeds limit")

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return 0, errOutputLimit
	}
	if len(p) > remaining {
		written, _ := b.Buffer.Write(p[:remaining])
		return written, errOutputLimit
	}
	return b.Buffer.Write(p)
}

// databaseCommandResult mirrors the generated application's JSON database
// result. This is a process boundary on purpose: nise delegates to application
// code and never imports or interprets its migrations.
type databaseCommandResult struct {
	Action  string  `json:"action"`
	State   string  `json:"state"`
	Current int64   `json:"current"`
	Target  int64   `json:"target"`
	Pending int64   `json:"pending"`
	Applied []int64 `json:"applied,omitempty"`
}

func (r databaseCommandResult) Human() string {
	if r.Action == "migrate" {
		return fmt.Sprintf("database migrated: current=%d target=%d applied=%d state=%s", r.Current, r.Target, len(r.Applied), r.State)
	}
	return fmt.Sprintf("database schema: current=%d target=%d pending=%d state=%s", r.Current, r.Target, r.Pending, r.State)
}

func dbCommand() *Command {
	return &Command{
		Name:  "db",
		Short: "Run the generated application's explicit database operations",
		Subcommands: []*Command{
			databaseActionCommand("migrate", "Apply every pending embedded migration"),
			databaseActionCommand("status", "Inspect schema compatibility without changing the database"),
		},
	}
}

func databaseActionCommand(action, short string) *Command {
	return &Command{
		Name:  action,
		Short: short,
		Run: func(ctx context.Context, env *Env) error {
			if len(env.Args) != 0 {
				return clierr.Usage(
					fmt.Sprintf("nise db %s takes no positional arguments", action),
					fmt.Sprintf("Run `nise db %s` without trailing arguments.", action),
				).WithCode("db.unexpected_arguments").WithDocs(dbDocs)
			}
			root, project, err := resolveDatabaseProject()
			if err != nil {
				return err
			}
			stdout, err := runApplicationDatabaseCommand(ctx, root, project, action)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					fmt.Sprintf("the generated application's db %s command failed", action),
					fmt.Sprintf("Run `go run ./cmd/%s db %s` directly to inspect its application-owned error, fix it, then retry.", project, action)).
					WithCode("db.application_failed").WithDocs(dbDocs)
			}
			result, err := decodeDatabaseCommandResult(stdout, action)
			if err != nil {
				return clierr.Wrap(err, clierr.ExitError,
					"the generated application returned an invalid database result",
					"Rebuild the application from current generated sources and retry.").
					WithCode("db.invalid_result").WithDocs(dbDocs)
			}
			env.Out.Result(result)
			return nil
		},
	}
}

func resolveDatabaseProject() (root, project string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", clierr.Wrap(err, clierr.ExitError,
			"could not determine the current directory",
			"Retry from a directory you have permission to read.").WithDocs(dbDocs)
	}
	root, found, err := findProjectRoot(wd)
	if err != nil {
		return "", "", clierr.Wrap(err, clierr.ExitError,
			fmt.Sprintf("could not search for %s", recipe.FileName),
			"Check filesystem permissions for the current directory and its parents.").WithDocs(dbDocs)
	}
	if !found {
		return "", "", clierr.Precondition(
			fmt.Sprintf("no %s found in the current directory or any parent", recipe.FileName),
			`Run "nise db" from inside a generated project.`).
			WithCode("db.no_project").WithDocs(dbDocs)
	}
	if _, err := recipe.Load(os.DirFS(root), recipe.FileName); err != nil {
		return "", "", clierr.Wrap(err, clierr.ExitPrecondition,
			fmt.Sprintf("%s does not parse", filepath.Join(root, recipe.FileName)),
			"Fix nise.json by hand or restore it from version control.").
			WithCode("db.invalid_recipe").WithDocs(dbDocs)
	}
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return "", "", clierr.Wrap(err, clierr.ExitPrecondition,
			"could not read cmd/ in the project root",
			"A generated project keeps its binary under cmd/<app>/.").
			WithCode("db.no_command").WithDocs(dbDocs)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != 1 {
		return "", "", clierr.Precondition(
			fmt.Sprintf("cmd/ must contain exactly one command directory; found %d (%s)", len(names), strings.Join(names, ", ")),
			"Keep one generated application command under cmd/<app>/ and retry.").
			WithCode("db.ambiguous_command").WithDocs(dbDocs)
	}
	return root, names[0], nil
}

func runApplicationDatabaseCommand(ctx context.Context, root, project, action string) (output []byte, retErr error) {
	scratch, err := os.MkdirTemp("", "nise-db-")
	if err != nil {
		return nil, fmt.Errorf("create database command scratch directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(scratch); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove database command scratch directory: %w", err))
		}
	}()

	environment := overlayEnvironment(os.Environ(), map[string]string{"GOTOOLCHAIN": "local"})
	binary := filepath.Join(scratch, "application"+binarySuffix())
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/"+project) // #nosec G204 -- project is one directory entry.
	build.Dir = root
	build.Env = environment
	build.Stdout = io.Discard
	build.Stderr = io.Discard
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("build generated application: %w", err)
	}

	command := exec.CommandContext(ctx, binary, "db", action, "--json") // #nosec G204 -- binary is a private scratch path and action is a fixed registry literal.
	command.Dir = root
	command.Env = environment
	stdout := boundedBuffer{limit: maxDatabaseCommandOutput}
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("run generated application database command: %w", err)
	}
	return bytes.Clone(stdout.Bytes()), nil
}

func decodeDatabaseCommandResult(data []byte, action string) (databaseCommandResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result databaseCommandResult
	if err := decoder.Decode(&result); err != nil {
		return databaseCommandResult{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return databaseCommandResult{}, errors.New("database result contains trailing data")
	}
	if result.Action != action {
		return databaseCommandResult{}, fmt.Errorf("database result action = %q, want %q", result.Action, action)
	}
	if result.State != "uninitialized" && result.State != "behind" && result.State != "current" {
		return databaseCommandResult{}, fmt.Errorf("database result state = %q", result.State)
	}
	if result.Current < 0 || result.Target < 1 || result.Current > result.Target || result.Pending != result.Target-result.Current {
		return databaseCommandResult{}, fmt.Errorf("database result has inconsistent versions current=%d target=%d pending=%d", result.Current, result.Target, result.Pending)
	}
	switch result.State {
	case "uninitialized":
		if result.Current != 0 {
			return databaseCommandResult{}, errors.New("uninitialized database result has a nonzero current version")
		}
	case "behind":
		if result.Current >= result.Target {
			return databaseCommandResult{}, errors.New("behind database result is not below its target")
		}
	case "current":
		if result.Current != result.Target {
			return databaseCommandResult{}, errors.New("current database result has not reached its target")
		}
	}
	if action == "migrate" && result.State != "current" {
		return databaseCommandResult{}, fmt.Errorf("migrate result state = %q, want current", result.State)
	}
	if action == "status" && len(result.Applied) != 0 {
		return databaseCommandResult{}, errors.New("status result unexpectedly contains applied versions")
	}
	var previous int64
	for index, version := range result.Applied {
		if version <= previous || version > result.Current || (index > 0 && version != previous+1) {
			return databaseCommandResult{}, errors.New("migrate result contains invalid or unordered applied versions")
		}
		previous = version
	}
	if len(result.Applied) > 0 && previous != result.Current {
		return databaseCommandResult{}, errors.New("migrate result applied versions do not end at the current version")
	}
	return result, nil
}
