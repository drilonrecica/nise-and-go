package dev

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// fakeExec is a scripted Exec: it answers Look from a set of installed
// binaries and Output/Run from a map keyed by the full command line.
type fakeExec struct {
	installed map[string]bool
	responses map[string]fakeResponse
	calls     []string
}

type fakeResponse struct {
	out string
	err error
}

func (f *fakeExec) Look(name string) (string, error) {
	if f.installed[name] {
		return "/usr/bin/" + name, nil
	}
	return "", exec.ErrNotFound
}

func (f *fakeExec) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, key)
	r, ok := f.responses[key]
	if !ok {
		return nil, fmt.Errorf("fakeExec: no scripted response for %q", key)
	}
	return []byte(r.out), r.err
}

func (f *fakeExec) Run(ctx context.Context, name string, args ...string) error {
	_, err := f.Output(ctx, name, args...)
	return err
}

func TestIdentifyEngine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		want    Engine
		wantOK  bool
	}{
		{"docker", "Docker version 29.6.2, build dfc4efb", EngineDocker, true},
		{"podman", "podman version 5.8.4", EnginePodman, true},
		{"podman shim named docker", "podman version 5.8.4", EnginePodman, true},
		{"podman emulating docker", "Docker version 5.8.4 (podman)", EnginePodman, true},
		{"unknown", "containerd v2.0.0", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := identifyEngine(tc.version)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("identifyEngine(%q) = (%q, %v), want (%q, %v)", tc.version, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestVersionNumber(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Docker version 29.6.2, build dfc4efb": "29.6.2",
		"podman version 5.8.4":                 "5.8.4",
		"weird output":                         "weird output",
	} {
		if got := versionNumber(in); got != want {
			t.Fatalf("versionNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectRuntimeIdentifiesAPodmanShimNamedDocker(t *testing.T) {
	t.Parallel()
	// The hazard the brief names: /usr/bin/docker is Podman on a great
	// many machines. Detection must believe what the binary says about
	// itself, not what it is called.
	ex := &fakeExec{
		installed: map[string]bool{"docker": true},
		responses: map[string]fakeResponse{
			"docker --version": {out: "podman version 5.8.4\n"},
			"docker info":      {out: ""},
		},
	}
	rt, err := DetectRuntime(context.Background(), ex, "")
	if err != nil {
		t.Fatalf("DetectRuntime: %v", err)
	}
	if rt.Engine != EnginePodman {
		t.Fatalf("Engine = %q, want %q", rt.Engine, EnginePodman)
	}
	if rt.Command != "docker" {
		t.Fatalf("Command = %q, want the binary that was actually found", rt.Command)
	}
	if !strings.Contains(rt.String(), "podman") || !strings.Contains(rt.String(), "docker") {
		t.Fatalf("String() = %q, want it to name both the engine and the binary", rt.String())
	}
}

func TestDetectRuntimeFallsThroughToPodman(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{
		installed: map[string]bool{"podman": true},
		responses: map[string]fakeResponse{
			"podman --version": {out: "podman version 5.8.4\n"},
			"podman info":      {out: "host: ..."},
		},
	}
	rt, err := DetectRuntime(context.Background(), ex, "")
	if err != nil {
		t.Fatalf("DetectRuntime: %v", err)
	}
	if rt.Engine != EnginePodman || rt.Command != "podman" || rt.Version != "5.8.4" {
		t.Fatalf("Runtime = %+v, want podman/podman/5.8.4", rt)
	}
}

func TestDetectRuntimeRejectsAnInstalledButUnusableEngine(t *testing.T) {
	t.Parallel()
	// A docker client with no daemon behind it. The failure must be
	// reported here, not three steps later when a container fails to run.
	ex := &fakeExec{
		installed: map[string]bool{"docker": true},
		responses: map[string]fakeResponse{
			"docker --version": {out: "Docker version 29.6.2, build dfc4efb\n"},
			"docker info":      {err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
		},
	}
	_, err := DetectRuntime(context.Background(), ex, "")
	if err == nil {
		t.Fatal("DetectRuntime accepted a runtime whose daemon is unreachable")
	}
	if !errors.Is(err, ErrNoContainerRuntime) {
		t.Fatalf("error %v does not wrap ErrNoContainerRuntime", err)
	}
	for _, want := range []string{"installed but not usable", "Cannot connect to the Docker daemon"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

func TestDetectRuntimeReportsEveryCandidateWhenNoneWork(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{installed: map[string]bool{}}
	_, err := DetectRuntime(context.Background(), ex, "")
	if !errors.Is(err, ErrNoContainerRuntime) {
		t.Fatalf("error %v does not wrap ErrNoContainerRuntime", err)
	}
	for _, want := range []string{"docker: not on PATH", "podman: not on PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

func TestDetectRuntimeHonorsAnExplicitChoice(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{
		installed: map[string]bool{"docker": true, "podman": true},
		responses: map[string]fakeResponse{
			"docker --version": {out: "Docker version 29.6.2, build x\n"},
			"docker info":      {},
			"podman --version": {out: "podman version 5.8.4\n"},
			"podman info":      {},
		},
	}
	rt, err := DetectRuntime(context.Background(), ex, "podman")
	if err != nil {
		t.Fatalf("DetectRuntime: %v", err)
	}
	if rt.Command != "podman" {
		t.Fatalf("Command = %q, want podman; the explicit choice was ignored", rt.Command)
	}
	for _, call := range ex.calls {
		if strings.HasPrefix(call, "docker ") {
			t.Fatalf("docker was probed despite an explicit --container-runtime=podman: %v", ex.calls)
		}
	}
}

func TestDetectRuntimeRejectsAnUnknownChoice(t *testing.T) {
	t.Parallel()
	_, err := DetectRuntime(context.Background(), &fakeExec{}, "containerd")
	if err == nil || !strings.Contains(err.Error(), "unknown container runtime") {
		t.Fatalf("error = %v, want it to reject the unknown runtime name", err)
	}
	if errors.Is(err, ErrNoContainerRuntime) {
		t.Fatal("a typo in --container-runtime must not be reported as a missing runtime")
	}
}
