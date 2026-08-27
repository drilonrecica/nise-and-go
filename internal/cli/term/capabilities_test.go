package term

import (
	"errors"
	"os"
	"testing"
	"time"
)

// fakeStat implements FileStat by returning a fixed Mode, so terminal
// detection is testable without an OS-level pty.
type fakeStat struct {
	charDevice bool
	statErr    error
}

func (f fakeStat) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	mode := os.FileMode(0)
	if f.charDevice {
		mode |= os.ModeCharDevice
	}
	return statFileInfo{mode}, nil
}

// statFileInfo is a minimal os.FileInfo implementation covering only
// Mode(), which is all isTerminal reads.
type statFileInfo struct{ mode os.FileMode }

func (s statFileInfo) Name() string       { return "fake" }
func (s statFileInfo) Size() int64        { return 0 }
func (s statFileInfo) Mode() os.FileMode  { return s.mode }
func (s statFileInfo) ModTime() time.Time { return time.Time{} }
func (s statFileInfo) IsDir() bool        { return false }
func (s statFileInfo) Sys() any           { return nil }

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    FileStat
		want bool
	}{
		{"nil stream is never a terminal", nil, false},
		{"character device is a terminal", fakeStat{charDevice: true}, true},
		{"regular file is not a terminal", fakeStat{charDevice: false}, false},
		{"stat error is not a terminal", fakeStat{statErr: errors.New("boom")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isTerminal(tt.f); got != tt.want {
				t.Errorf("isTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stdout FileStat
		stderr FileStat
		env    map[string]string
		want   Capabilities
	}{
		{
			name:   "no environment, non-terminal streams",
			stdout: fakeStat{charDevice: false},
			stderr: fakeStat{charDevice: false},
			env:    nil,
			want:   Capabilities{},
		},
		{
			name:   "interactive terminal, no env",
			stdout: fakeStat{charDevice: true},
			stderr: fakeStat{charDevice: true},
			env:    nil,
			want:   Capabilities{StdoutIsTerminal: true, StderrIsTerminal: true},
		},
		{
			name:   "NO_COLOR set to empty string does not disable color",
			stdout: fakeStat{charDevice: true},
			env:    map[string]string{"NO_COLOR": ""},
			want:   Capabilities{StdoutIsTerminal: true},
		},
		{
			name:   "NO_COLOR set to any value disables color",
			stdout: fakeStat{charDevice: true},
			env:    map[string]string{"NO_COLOR": "1"},
			want:   Capabilities{StdoutIsTerminal: true, NoColor: true},
		},
		{
			name: "TERM=dumb detected",
			env:  map[string]string{"TERM": "dumb"},
			want: Capabilities{TermDumb: true},
		},
		{
			name: "CLICOLOR=0 sets CLIColorDisabled, not CLIColorEnabled",
			env:  map[string]string{"CLICOLOR": "0"},
			want: Capabilities{CLIColorDisabled: true},
		},
		{
			name: "CLICOLOR=1 sets CLIColorEnabled",
			env:  map[string]string{"CLICOLOR": "1"},
			want: Capabilities{CLIColorEnabled: true},
		},
		{
			name: "CLICOLOR_FORCE=0 does not force",
			env:  map[string]string{"CLICOLOR_FORCE": "0"},
			want: Capabilities{},
		},
		{
			name: "CLICOLOR_FORCE=1 forces",
			env:  map[string]string{"CLICOLOR_FORCE": "1"},
			want: Capabilities{CLIColorForce: true},
		},
		{
			name: "CI set disables animation flag",
			env:  map[string]string{"CI": "true"},
			want: Capabilities{CI: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string { return tt.env[key] }
			got := Detect(tt.stdout, tt.stderr, getenv)
			if got != tt.want {
				t.Errorf("Detect() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDetectNilGetenv(t *testing.T) {
	t.Parallel()
	got := Detect(nil, nil, nil)
	if got != (Capabilities{}) {
		t.Errorf("Detect(nil, nil, nil) = %+v, want zero value", got)
	}
}

func TestColorEnabledPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps Capabilities
		want bool
	}{
		{"force wins even off a terminal", Capabilities{CLIColorForce: true, StdoutIsTerminal: false, NoColor: true}, true},
		{"NO_COLOR beats a terminal", Capabilities{StdoutIsTerminal: true, NoColor: true}, false},
		{"TERM=dumb beats a terminal", Capabilities{StdoutIsTerminal: true, TermDumb: true}, false},
		{"not a terminal, no overrides", Capabilities{StdoutIsTerminal: false}, false},
		{"CLICOLOR set on a terminal", Capabilities{StdoutIsTerminal: true, CLIColorEnabled: true}, true},
		{"plain terminal defaults to color", Capabilities{StdoutIsTerminal: true}, true},
		// Regression: CLICOLOR=0 must disable color even on an
		// interactive terminal. Verified live before this fix: a real
		// TTY with CLICOLOR=0 still returned ColorEnabled() == true,
		// because Capabilities had no way to distinguish "CLICOLOR
		// unset" from "CLICOLOR=0" — both produced CLIColor: false and
		// fell through to the same "otherwise → true" default.
		{"CLICOLOR=0 disables color even on a terminal", Capabilities{StdoutIsTerminal: true, CLIColorDisabled: true}, false},
		{"CLICOLOR=0 off a terminal is still false (already false, but must not become force-true)", Capabilities{StdoutIsTerminal: false, CLIColorDisabled: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.caps.ColorEnabled(); got != tt.want {
				t.Errorf("ColorEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnimationEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps Capabilities
		want bool
	}{
		{"interactive terminal, no CI, not dumb", Capabilities{StdoutIsTerminal: true}, true},
		{"not a terminal", Capabilities{StdoutIsTerminal: false}, false},
		{"CI disables animation even on a terminal", Capabilities{StdoutIsTerminal: true, CI: true}, false},
		{"TERM=dumb disables animation even on a terminal", Capabilities{StdoutIsTerminal: true, TermDumb: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.caps.AnimationEnabled(); got != tt.want {
				t.Errorf("AnimationEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
