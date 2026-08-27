package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile writes content at a path under root, creating directories.
func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// projectTree builds a tree shaped like the one `nise new` produces,
// including the directories a watcher must not descend into.
func projectTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/demoapp\n")
	writeFile(t, root, "go.sum", "")
	writeFile(t, root, "cmd/demoapp/main.go", "package main\n")
	writeFile(t, root, "internal/app/app.go", "package app\n")
	writeFile(t, root, "internal/platform/config/config.go", "package config\n")
	writeFile(t, root, "db/migrations/00001_init.sql", "-- init\n")
	// Noise that must be ignored.
	writeFile(t, root, "internal/platform/webui/embedded/client/index.html", "<html>")
	writeFile(t, root, "frontend/src/routes/+page.svelte", "<main/>")
	writeFile(t, root, "frontend/node_modules/pkg/thing.go", "package pkg\n")
	writeFile(t, root, ".git/objects/ab/cdef", "binary")
	writeFile(t, root, "internal/app/README.md", "notes")
	return root
}

func TestWatcherScanSeesSourceAndIgnoresTheRest(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{Root: root}
	snap := w.Scan()

	for _, rel := range []string{
		"go.mod", "go.sum",
		"cmd/demoapp/main.go",
		"internal/app/app.go",
		"internal/platform/config/config.go",
		"db/migrations/00001_init.sql",
	} {
		if _, ok := snap[filepath.Join(root, rel)]; !ok {
			t.Fatalf("Scan did not see %s", rel)
		}
	}
	for _, rel := range []string{
		"internal/platform/webui/embedded/client/index.html",
		"frontend/src/routes/+page.svelte",
		"frontend/node_modules/pkg/thing.go",
		".git/objects/ab/cdef",
		"internal/app/README.md",
	} {
		if _, ok := snap[filepath.Join(root, rel)]; ok {
			t.Fatalf("Scan descended into or matched %s, which it must ignore", rel)
		}
	}
}

func TestSnapshotChangedDetectsEveryDirection(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{Root: root}
	before := w.Scan()

	// Modified. The write changes the size, so this is detected even where
	// the filesystem's modification-time granularity is coarse.
	writeFile(t, root, "internal/app/app.go", "package app\n\nfunc New() {}\n")
	// Added.
	writeFile(t, root, "internal/app/wire.go", "package app\n")
	// Removed.
	if err := os.Remove(filepath.Join(root, "db/migrations/00001_init.sql")); err != nil {
		t.Fatalf("removing a file: %v", err)
	}

	changed := before.Changed(w.Scan())
	if len(changed) != 3 {
		t.Fatalf("Changed() = %v, want three paths (modified, added, removed)", changed)
	}
	for _, want := range []string{"app.go", "wire.go", "00001_init.sql"} {
		found := false
		for _, got := range changed {
			if strings.HasSuffix(got, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("Changed() = %v, want it to include %s", changed, want)
		}
	}
	// The result must be sorted, so a log line built from it is stable.
	for i := 1; i < len(changed); i++ {
		if changed[i-1] > changed[i] {
			t.Fatalf("Changed() = %v, want it sorted", changed)
		}
	}
}

func TestSnapshotChangedIsEmptyForAnUnchangedTree(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{Root: root}
	if got := w.Scan().Changed(w.Scan()); len(got) != 0 {
		t.Fatalf("Changed() = %v on an unchanged tree, want none", got)
	}
}

func TestWatcherRunSignalsAChangeAndThenGoesQuiet(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{Root: root, Interval: 5 * time.Millisecond}
	changes := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx, changes) }()

	// Keep editing until the watcher notices. Run takes its baseline
	// snapshot when it starts, so a single write racing that first sweep
	// could legitimately be folded into the baseline; repeating the edit
	// removes the race without making the test depend on a sleep.
	edited := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-edited:
				return
			default:
			}
			// Not writeFile: t.Fatalf may only be called from the test's
			// own goroutine, and a failed write here is covered by the
			// timeout below anyway.
			_ = os.WriteFile(filepath.Join(root, "internal", "app", "app.go"),
				[]byte(strings.Repeat("x", i+1)), 0o600)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		close(edited)
		t.Fatal("the watcher never reported a change")
	}
	close(edited)
	time.Sleep(30 * time.Millisecond) // let the last edit settle into a sweep
	select {                          // drain anything the final edit queued
	case <-changes:
	default:
	}

	// A tree that stops changing must stop signalling. A watcher that
	// reported every sweep would restart the server forever.
	select {
	case <-changes:
		t.Fatal("the watcher reported a change on an unchanged tree")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Watcher.Run did not return after its context was canceled")
	}
}

func TestWatcherRunNeverBlocksOnAFullChannel(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{Root: root, Interval: time.Millisecond}
	// A channel nobody drains, exactly as a stalled rebuild would leave it.
	changes := make(chan struct{}, 1)
	changes <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx, changes) }()

	for i := 0; i < 5; i++ {
		writeFile(t, root, "internal/app/app.go", strings.Repeat("x", i+1))
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the watcher blocked on a full change channel")
	}
}

func TestWatcherReportsWalkErrorsWithoutGivingUp(t *testing.T) {
	t.Parallel()
	root := projectTree(t)
	w := &Watcher{
		Root:    root,
		Include: []string{"cmd", "does-not-exist", "internal"},
		OnError: func(err error) { t.Errorf("unexpected OnError: %v", err) },
	}
	// A missing include directory is normal — a Slice 1 project has no
	// db/ — so it is skipped silently, and the sweep of the directories
	// that do exist must complete regardless.
	snap := w.Scan()
	if _, ok := snap[filepath.Join(root, "internal/app/app.go")]; !ok {
		t.Fatal("a missing include directory stopped the sweep of the others")
	}
	if _, ok := snap[filepath.Join(root, "cmd/demoapp/main.go")]; !ok {
		t.Fatal("a missing include directory stopped the sweep of an earlier one")
	}
}

// BenchmarkWatcherScan measures one sweep of a generated project's Go tree.
// Its result is what docs/commands/dev.md quotes as the watcher's cost, and
// it is the number that decides whether the no-dependency polling design
// stays defensible.
func BenchmarkWatcherScan(b *testing.B) {
	root := b.TempDir()
	// A tree the size of a generated project after a few milestones:
	// 40 packages of 8 files each, plus the noise directories.
	for pkg := 0; pkg < 40; pkg++ {
		for file := 0; file < 8; file++ {
			path := filepath.Join(root, "internal", "features", "f"+string(rune('a'+pkg%26))+string(rune('0'+pkg/26)), "f"+string(rune('0'+file))+".go")
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("package f\n"), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	w := &Watcher{Root: root}
	if n := len(w.Scan()); n < 300 {
		b.Fatalf("the benchmark tree has only %d files; it is not representative", n)
	}
	b.ReportAllocs()
	for b.Loop() {
		w.Scan()
	}
}
