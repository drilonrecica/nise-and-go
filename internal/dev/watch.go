package dev

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Watcher notices source changes by polling file metadata.
//
// # Why polling, and not fsnotify
//
// The framework has zero runtime dependencies (Constraints §2), and
// `fsnotify` would be the first. What it buys is sub-millisecond latency;
// what it costs, besides the dependency, is a per-directory watch
// descriptor (a real limit on Linux, where a large tree can exhaust
// `fs.inotify.max_user_watches`), platform-specific behavior on every
// operating system, and an editor's rename-over-original save producing a
// different event sequence on each one.
//
// The alternative is a stat sweep, and the question is only what it costs.
// Measured on the tree `nise new` generates (see BenchmarkWatcherScan and
// docs/commands/dev.md), one sweep is well under a millisecond, so at the
// default interval the watcher's duty cycle is a fraction of one percent of
// one core. The latency it adds — at most one poll interval, plus the
// supervisor's debounce — is small next to the `go build` that follows it.
//
// If a project ever grows a tree where this stops being true, the honest
// fix is a longer interval, not a dependency.
type Watcher struct {
	// Root is the project root every relative path below is joined to.
	Root string
	// Include are the subtrees to walk, relative to Root. Empty selects
	// DefaultWatchDirs.
	Include []string
	// Files are individual files to watch, relative to Root. Empty selects
	// DefaultWatchFiles.
	Files []string
	// SkipDirs are directory base names never descended into. Empty
	// selects DefaultSkipDirs.
	SkipDirs []string
	// Extensions are the file suffixes that count as source. Empty selects
	// DefaultWatchExtensions.
	Extensions []string
	// Interval is the poll period. Zero selects DefaultPollInterval.
	Interval time.Duration
	// OnError, when non-nil, receives a walk error. A sweep that cannot
	// read one directory still reports every other change it found.
	OnError func(error)
}

// Watcher defaults.
var (
	// DefaultWatchDirs are the Go subtrees of a generated project.
	// frontend/ is deliberately absent: Vite watches it far better than a
	// stat sweep could, and a rebuild of the Go binary is the wrong
	// response to a .svelte edit.
	DefaultWatchDirs = []string{"cmd", "internal", "db"}
	// DefaultWatchFiles are the root files that change what `go build`
	// produces without living under a watched subtree.
	DefaultWatchFiles = []string{"go.mod", "go.sum"}
	// DefaultSkipDirs are never descended into. node_modules and
	// .svelte-kit are enormous and churn constantly; embedded/client is
	// the frontend build's output, rewritten on every `pnpm build`, and
	// rebuilding Go because Go's own embed target changed would be a loop.
	DefaultSkipDirs = []string{".git", "node_modules", ".svelte-kit", "bin", "embedded", "testdata"}
	// DefaultWatchExtensions are the suffixes that change the binary.
	DefaultWatchExtensions = []string{".go", ".mod", ".sum", ".sql"}
)

// DefaultPollInterval is how often the watcher sweeps. It is tuned against
// two numbers: a human notices latency somewhere above 100 ms, and the
// sweep itself costs well under a millisecond on a generated project, so
// 300 ms keeps the loop feeling immediate at a duty cycle under 0.3% of one
// core. Every extra millisecond of latency here is invisible next to the
// `go build` that follows.
const DefaultPollInterval = 300 * time.Millisecond

// stamp is the metadata a sweep compares. Content is deliberately not
// hashed: reading every file on every sweep would cost orders of magnitude
// more, and the failure mode of metadata comparison — a file rewritten
// within the same modification-time granularity and to the same length —
// costs one missed rebuild that the next save fixes.
type stamp struct {
	size    int64
	modTime int64
}

// Snapshot is one sweep's view of the watched tree.
type Snapshot map[string]stamp

// Changed reports the sorted paths that differ between two snapshots, in
// either direction: added, removed, or modified.
func (s Snapshot) Changed(next Snapshot) []string {
	var out []string
	for path, was := range s {
		now, ok := next[path]
		if !ok || now != was {
			out = append(out, path)
		}
	}
	for path := range next {
		if _, ok := s[path]; !ok {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// Scan takes one snapshot. It never fails as a whole: a directory it cannot
// read is reported through OnError and skipped, because a transient
// permission or race error on one path is not a reason to stop watching the
// rest of the project.
func (w *Watcher) Scan() Snapshot {
	snap := make(Snapshot, 256)
	skip := make(map[string]bool, len(w.skipDirs()))
	for _, d := range w.skipDirs() {
		skip[d] = true
	}
	exts := w.extensions()

	for _, dir := range w.includeDirs() {
		root := filepath.Join(w.Root, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A missing include directory is normal (a Slice 1
				// project has no db/ yet); anything else is reported.
				if d != nil && w.OnError != nil {
					w.OnError(err)
				}
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if skip[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if !hasSuffixAny(path, exts) {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			snap[path] = stamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
			return nil
		})
		if err != nil && w.OnError != nil {
			w.OnError(err)
		}
	}

	for _, name := range w.includeFiles() {
		path := filepath.Join(w.Root, name)
		info, err := os.Stat(path) // #nosec G304 -- path is joined from the project root and this package's own fixed file names.
		if err != nil {
			continue
		}
		snap[path] = stamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
	}
	return snap
}

// Run sweeps every Interval until ctx is canceled, sending on changes each
// time the snapshot differs from the previous one.
//
// The send is non-blocking against a channel the caller is expected to give
// a buffer of one: the consumer debounces anyway, so a change that arrives
// while one is already queued adds nothing, and dropping it is what keeps a
// slow rebuild from stalling the watcher behind a full channel.
func (w *Watcher) Run(ctx context.Context, changes chan<- struct{}) {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	previous := w.Scan()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next := w.Scan()
		if len(previous.Changed(next)) > 0 {
			select {
			case changes <- struct{}{}:
			default:
			}
		}
		previous = next
	}
}

func (w *Watcher) includeDirs() []string {
	if len(w.Include) == 0 {
		return DefaultWatchDirs
	}
	return w.Include
}

func (w *Watcher) includeFiles() []string {
	if len(w.Files) == 0 {
		return DefaultWatchFiles
	}
	return w.Files
}

func (w *Watcher) skipDirs() []string {
	if len(w.SkipDirs) == 0 {
		return DefaultSkipDirs
	}
	return w.SkipDirs
}

func (w *Watcher) extensions() []string {
	if len(w.Extensions) == 0 {
		return DefaultWatchExtensions
	}
	return w.Extensions
}

// hasSuffixAny reports whether path ends with any of suffixes.
func hasSuffixAny(path string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(path, s) {
			return true
		}
	}
	return false
}
