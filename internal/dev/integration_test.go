//go:build dev_integration

// Package dev's container integration test.
//
// It is behind the dev_integration build tag because it starts a real
// PostgreSQL container, pulls a real image the first time, and leaves a
// named volume behind — none of which belongs in the default `go test
// ./...` a contributor runs on every save, and none of which a CI job
// without a container runtime could pass.
//
// Run it with:
//
//	go test -tags dev_integration ./internal/dev/ -run Integration -v
//
// Add -count=1 to defeat the test cache, and pass NISE_DEV_TEST_RUNTIME to
// pick an engine explicitly:
//
//	NISE_DEV_TEST_RUNTIME=podman go test -tags dev_integration ./internal/dev/ -run Integration -v
//
// The test cleans up after itself: it removes both the container and the
// volume it created, using a project name of its own that cannot collide
// with a developer's real project.

package dev

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// integrationProject is the project name the test derives its container and
// volume names from. It is deliberately unlike any real project name.
const integrationProject = "nisedevselftest"

func TestIntegrationPostgresLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ex := SystemExec{}
	rt, err := DetectRuntime(ctx, ex, os.Getenv("NISE_DEV_TEST_RUNTIME"))
	if err != nil {
		t.Skipf("no usable container runtime: %v", err)
	}
	t.Logf("using %s", rt)

	// A free loopback port, so a developer's own nise dev on 5432 is not
	// disturbed by running this test.
	port := freeLoopbackPort(t)
	pg := NewPostgres(integrationProject, "", "127.0.0.1", port)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := ex.Run(cleanupCtx, rt.Command, "rm", "--force", pg.ContainerName()); err != nil {
			t.Logf("removing the container: %v", err)
		}
		if err := ex.Run(cleanupCtx, rt.Command, "volume", "rm", "--force", pg.VolumeName()); err != nil {
			t.Logf("removing the volume: %v", err)
		}
	})

	var progress []string
	if err := pg.Up(ctx, ex, rt, func(msg string) { progress = append(progress, msg) }); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Logf("Up: %s", strings.Join(progress, "; "))

	if err := pg.VerifyPublishedPort(ctx, ex, rt); err != nil {
		t.Fatalf("VerifyPublishedPort: %v", err)
	}

	start := time.Now()
	if err := pg.WaitReady(ctx, ex, rt, 2*time.Minute, 250*time.Millisecond); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	t.Logf("ready after %s", time.Since(start).Round(time.Millisecond))

	// The health gate must mean what it says: a psql connected through the
	// container answers immediately after WaitReady returns.
	out, err := ex.Output(ctx, rt.Command, "exec", pg.ContainerName(),
		"psql", "--username", pg.User(), "--dbname", pg.Database(),
		"--tuples-only", "--no-align", "--command", "select current_database()")
	if err != nil {
		t.Fatalf("psql immediately after WaitReady: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != pg.Database() {
		t.Fatalf("current_database() = %q, want %q", got, pg.Database())
	}

	// A second Up on a running container must be a no-op, not a second
	// container.
	progress = nil
	if err := pg.Up(ctx, ex, rt, func(msg string) { progress = append(progress, msg) }); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(progress) != 1 || !strings.Contains(progress[0], "already running") {
		t.Fatalf("second Up reported %v, want a single already-running line", progress)
	}

	// Stop, then Up again: the data volume survives, so the same cluster
	// comes back rather than being initialized from scratch.
	if _, err := ex.Output(ctx, rt.Command, "exec", pg.ContainerName(),
		"psql", "--username", pg.User(), "--dbname", pg.Database(),
		"--command", "create table survives (id int)"); err != nil {
		t.Fatalf("creating a table: %v", err)
	}
	if err := pg.Stop(ctx, ex, rt); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := pg.Up(ctx, ex, rt, nil); err != nil {
		t.Fatalf("Up after Stop: %v", err)
	}
	if err := pg.WaitReady(ctx, ex, rt, 2*time.Minute, 250*time.Millisecond); err != nil {
		t.Fatalf("WaitReady after restart: %v", err)
	}
	if _, err := ex.Output(ctx, rt.Command, "exec", pg.ContainerName(),
		"psql", "--username", pg.User(), "--dbname", pg.Database(),
		"--command", "select count(*) from survives"); err != nil {
		t.Fatalf("the table did not survive the restart; the data volume is not being reused: %v", err)
	}
}

// TestIntegrationPortMismatchIsDetected covers the failure a developer
// actually hits: a container left over from an earlier run publishes a
// different port than the flag now asks for.
func TestIntegrationPortMismatchIsDetected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ex := SystemExec{}
	rt, err := DetectRuntime(ctx, ex, os.Getenv("NISE_DEV_TEST_RUNTIME"))
	if err != nil {
		t.Skipf("no usable container runtime: %v", err)
	}

	first := NewPostgres(integrationProject+"b", "", "127.0.0.1", freeLoopbackPort(t))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = ex.Run(cleanupCtx, rt.Command, "rm", "--force", first.ContainerName())
		_ = ex.Run(cleanupCtx, rt.Command, "volume", "rm", "--force", first.VolumeName())
	})
	if err := first.Up(ctx, ex, rt, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Same project, different requested port: the names collide with the
	// container that is already running.
	second := NewPostgres(integrationProject+"b", "", "127.0.0.1", freeLoopbackPort(t))
	err = second.VerifyPublishedPort(ctx, ex, rt)
	if err == nil {
		t.Fatal("VerifyPublishedPort accepted a container publishing a different port")
	}
	if !strings.Contains(err.Error(), first.HostPort) {
		t.Fatalf("error %v does not name the port the container actually publishes", err)
	}
}

// freeLoopbackPort reuses the helper the unit tests already have.
func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	addr := reserveClosedPort(t)
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}
