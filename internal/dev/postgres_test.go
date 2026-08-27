package dev

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSlugIsDeterministicAndSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "demoapp", "demoapp"},
		{"mixed case", "DemoApp", "demoapp"},
		{"spaces and punctuation", "My Great App!", "my-great-app"},
		{"underscores", "my_great_app", "my-great-app"},
		{"leading and trailing junk", "--__App__--", "app"},
		{"repeated separators", "a///b", "a-b"},
		{"unicode", "café-app", "caf-app"},
		{"digits first", "12factor", "app-12factor"},
		{"empty", "", "app"},
		{"only punctuation", "!!!", "app"},
		{"overlong", strings.Repeat("x", 80), strings.Repeat("x", 40)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := slug(tc.in)
			if got != tc.want {
				t.Fatalf("slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got != slug(tc.in) {
				t.Fatal("slug is not deterministic")
			}
			for _, r := range got {
				isLower := r >= 'a' && r <= 'z'
				isDigit := r >= '0' && r <= '9'
				if !isLower && !isDigit && r != '-' {
					t.Fatalf("slug(%q) = %q contains %q, which is not safe in a container or role name", tc.in, got, r)
				}
			}
		})
	}
}

func TestPostgresNamesAreDeterministicAndDerived(t *testing.T) {
	t.Parallel()
	p := NewPostgres("Demo App", "", "", "")
	if got, want := p.ContainerName(), "nise-dev-demo-app-postgres"; got != want {
		t.Fatalf("ContainerName() = %q, want %q", got, want)
	}
	if got, want := p.VolumeName(), "nise-dev-demo-app-pgdata"; got != want {
		t.Fatalf("VolumeName() = %q, want %q", got, want)
	}
	if p.Image != DefaultPostgresImage {
		t.Fatalf("Image = %q, want the pinned default %q", p.Image, DefaultPostgresImage)
	}
	// A different project must never collide with this one.
	other := NewPostgres("other", "", "", "")
	if other.ContainerName() == p.ContainerName() || other.VolumeName() == p.VolumeName() {
		t.Fatal("two projects share a container or volume name")
	}
}

func TestDefaultPostgresImageIsPinnedAndFullyQualified(t *testing.T) {
	t.Parallel()
	// A floating tag means two developers on the same commit run
	// different PostgreSQL versions, and the registry prefix is what makes
	// the reference resolve identically under Podman, which has no
	// implicit docker.io default.
	if strings.HasSuffix(DefaultPostgresImage, ":latest") || !strings.Contains(DefaultPostgresImage, ":") {
		t.Fatalf("DefaultPostgresImage = %q, want an exact pinned tag", DefaultPostgresImage)
	}
	if !strings.HasPrefix(DefaultPostgresImage, "docker.io/") {
		t.Fatalf("DefaultPostgresImage = %q, want a fully qualified registry", DefaultPostgresImage)
	}
}

func TestRedactedDSNHidesThePasswordAndNothingElse(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "127.0.0.1", "5432")
	real, shown := p.DSN(), p.RedactedDSN()

	if !strings.Contains(real, p.credential()) {
		t.Fatal("DSN does not carry the real credential; the application child could not connect")
	}
	if strings.Contains(shown, p.credential()) {
		t.Fatalf("RedactedDSN leaks the password: %q", shown)
	}
	if !strings.Contains(shown, redactedPassword) {
		t.Fatalf("RedactedDSN = %q, want it to carry %q", shown, redactedPassword)
	}
	// Everything else must survive, or the printed string is not usable
	// as a connection string to paste into psql with your own password.
	for _, want := range []string{"postgres://demoapp:", "@127.0.0.1:5432", "/demoapp", "sslmode=disable"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("RedactedDSN = %q, want it to contain %q", shown, want)
		}
	}
	if got, want := strings.Replace(real, p.credential(), redactedPassword, 1), shown; got != want {
		t.Fatalf("RedactedDSN differs from DSN by more than the password:\n got %q\nwant %q", want, got)
	}
}

func TestPostgresCreateArgsPublishOnLoopbackOnly(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "", "")
	args := strings.Join(p.createArgs(), " ")

	// The development database has a derived, non-secret password. It must
	// therefore be unreachable from anywhere but this machine.
	if !strings.Contains(args, "--publish 127.0.0.1:5432:5432") {
		t.Fatalf("createArgs = %q, want the port published on loopback only", args)
	}
	if strings.Contains(args, "0.0.0.0") || strings.Contains(args, "--publish 5432:5432") {
		t.Fatalf("createArgs = %q, want no publicly reachable publish", args)
	}
	for _, want := range []string{
		"run --detach",
		"--name " + p.ContainerName(),
		"--volume " + p.VolumeName() + ":" + containerDataDir,
		"POSTGRES_USER=demoapp",
		"POSTGRES_DB=demoapp",
		DefaultPostgresImage,
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("createArgs = %q, want it to contain %q", args, want)
		}
	}
	if strings.Contains(args, "--restart") {
		t.Fatalf("createArgs = %q, want no --restart policy on a development container", args)
	}
}

func TestPostgresUpCreatesThenStartsThenNoOps(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "", "")
	rt := Runtime{Engine: EngineDocker, Command: "docker"}
	inspect := "docker inspect --format {{.State.Running}} " + p.ContainerName()

	// First run: the container does not exist.
	ex := &fakeExec{responses: map[string]fakeResponse{
		inspect:                                  {err: errors.New("no such object")},
		"docker volume create " + p.VolumeName(): {},
		"docker " + strings.Join(p.createArgs(), " "): {},
	}}
	if err := p.Up(context.Background(), ex, rt, nil); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if len(ex.calls) != 3 {
		t.Fatalf("first Up made %d calls, want 3 (inspect, volume create, run): %v", len(ex.calls), ex.calls)
	}

	// Second run: the container exists but is stopped.
	ex = &fakeExec{responses: map[string]fakeResponse{
		inspect:                             {out: "false\n"},
		"docker start " + p.ContainerName(): {},
	}}
	if err := p.Up(context.Background(), ex, rt, nil); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(ex.calls) != 2 || !strings.HasPrefix(ex.calls[1], "docker start") {
		t.Fatalf("second Up did not simply start the existing container: %v", ex.calls)
	}

	// Third run: it is already running.
	ex = &fakeExec{responses: map[string]fakeResponse{inspect: {out: "true\n"}}}
	if err := p.Up(context.Background(), ex, rt, nil); err != nil {
		t.Fatalf("third Up: %v", err)
	}
	if len(ex.calls) != 1 {
		t.Fatalf("third Up touched a running container: %v", ex.calls)
	}
}

func TestPostgresWaitReadyGatesOnPgIsready(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "", "")
	rt := Runtime{Engine: EnginePodman, Command: "podman"}
	ready := "podman " + strings.Join(p.readyArgs(), " ")

	attempts := 0
	ex := &countingExec{fn: func(key string) error {
		if key != ready {
			t.Fatalf("unexpected call %q", key)
		}
		attempts++
		if attempts < 3 {
			return errors.New("connection refused")
		}
		return nil
	}}
	if err := p.WaitReady(context.Background(), ex, rt, 2*time.Second, time.Millisecond); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestPostgresWaitReadyTimesOutWithTheLastReason(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "", "")
	rt := Runtime{Engine: EngineDocker, Command: "docker"}
	ex := &countingExec{fn: func(string) error { return errors.New("the database system is starting up") }}

	err := p.WaitReady(context.Background(), ex, rt, 20*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("WaitReady returned nil after the timeout elapsed")
	}
	if !strings.Contains(err.Error(), "did not become ready") ||
		!strings.Contains(err.Error(), "the database system is starting up") {
		t.Fatalf("error = %v, want it to name the timeout and the last reason", err)
	}
}

func TestPostgresStopCommandIsCopyPastable(t *testing.T) {
	t.Parallel()
	p := NewPostgres("demoapp", "", "", "")
	rt := Runtime{Engine: EnginePodman, Command: "docker"}
	if got, want := p.StopCommand(rt), "docker stop nise-dev-demoapp-postgres"; got != want {
		t.Fatalf("StopCommand() = %q, want %q (the binary the developer actually has)", got, want)
	}
}

// countingExec answers every call through one function.
type countingExec struct{ fn func(key string) error }

func (c *countingExec) Look(name string) (string, error) { return "/usr/bin/" + name, nil }

func (c *countingExec) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	return nil, c.fn(strings.TrimSpace(name + " " + strings.Join(args, " ")))
}

func (c *countingExec) Run(ctx context.Context, name string, args ...string) error {
	_, err := c.Output(ctx, name, args...)
	return err
}
