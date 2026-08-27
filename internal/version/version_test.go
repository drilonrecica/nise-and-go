package version

import (
	"encoding/json"
	"runtime/debug"
	"testing"
)

// buildInfo fabricates the subset of debug.BuildInfo that newInfo reads.
func buildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	bi.Main.Version = mainVersion
	for k, v := range settings {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return bi
}

func TestNewInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		version, commit, date string
		bi                    *debug.BuildInfo
		ok                    bool
		want                  Info
	}{
		{
			name:    "ldflags stamped values win over build info",
			version: "v1.2.3",
			commit:  "stamped-sha",
			date:    "2026-08-27T00:00:00Z",
			bi: buildInfo("v9.9.9", map[string]string{
				"vcs.revision": "embedded-sha",
				"vcs.time":     "1999-01-01T00:00:00Z",
			}),
			ok: true,
			want: Info{
				Version: "v1.2.3",
				Commit:  "stamped-sha",
				Date:    "2026-08-27T00:00:00Z",
			},
		},
		{
			name: "go install build falls back to module version and vcs settings",
			bi: buildInfo("v0.3.0", map[string]string{
				"vcs.revision": "1a2b3c4d",
				"vcs.time":     "2026-08-01T12:00:00Z",
			}),
			ok: true,
			want: Info{
				Version: "v0.3.0",
				Commit:  "1a2b3c4d",
				Date:    "2026-08-01T12:00:00Z",
			},
		},
		{
			name: "go install of a pseudo-version reports the pseudo-version",
			bi:   buildInfo("v0.0.0-20260827000000-1a2b3c4d5e6f", nil),
			ok:   true,
			want: Info{Version: "v0.0.0-20260827000000-1a2b3c4d5e6f"},
		},
		{
			name: "source checkout build reports dev but keeps vcs metadata",
			bi: buildInfo("(devel)", map[string]string{
				"vcs.revision": "deadbeef",
				"vcs.time":     "2026-08-27T09:00:00Z",
			}),
			ok: true,
			want: Info{
				Version: "dev",
				Commit:  "deadbeef",
				Date:    "2026-08-27T09:00:00Z",
			},
		},
		{
			name:    "partial stamping fills the gaps from build info",
			version: "v2.0.0",
			bi: buildInfo("(devel)", map[string]string{
				"vcs.revision": "cafef00d",
				"vcs.time":     "2026-08-27T09:00:00Z",
			}),
			ok: true,
			want: Info{
				Version: "v2.0.0",
				Commit:  "cafef00d",
				Date:    "2026-08-27T09:00:00Z",
			},
		},
		{
			name: "no build info at all reports dev",
			bi:   nil,
			ok:   false,
			want: Info{Version: "dev"},
		},
		{
			name: "empty main version reports dev",
			bi:   buildInfo("", nil),
			ok:   true,
			want: Info{Version: "dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newInfo(tt.version, tt.commit, tt.date, tt.bi, tt.ok)
			if got != tt.want {
				t.Errorf("newInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetNeverReturnsEmptyVersion(t *testing.T) {
	t.Parallel()
	if got := Get(); got.Version == "" {
		t.Errorf("Get() returned empty Version: %+v", got)
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "full",
			info: Info{Version: "v1.2.3", Commit: "1a2b3c4d", Date: "2026-08-27T00:00:00Z"},
			want: "nise v1.2.3 (commit 1a2b3c4d, built 2026-08-27T00:00:00Z)",
		},
		{
			name: "version only",
			info: Info{Version: "dev"},
			want: "nise dev",
		},
		{
			name: "commit without date",
			info: Info{Version: "dev", Commit: "deadbeef"},
			want: "nise dev (commit deadbeef)",
		},
		{
			name: "date without commit",
			info: Info{Version: "v0.1.0", Date: "2026-08-27T00:00:00Z"},
			want: "nise v0.1.0 (built 2026-08-27T00:00:00Z)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.info.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInfoJSONShape(t *testing.T) {
	t.Parallel()

	full := Info{Version: "v1.2.3", Commit: "1a2b3c4d", Date: "2026-08-27T00:00:00Z"}
	b, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("json.Marshal(%+v): %v", full, err)
	}
	want := `{"version":"v1.2.3","commit":"1a2b3c4d","date":"2026-08-27T00:00:00Z"}`
	if string(b) != want {
		t.Errorf("json.Marshal(full) = %s, want %s", b, want)
	}

	minimal := Info{Version: "dev"}
	b, err = json.Marshal(minimal)
	if err != nil {
		t.Fatalf("json.Marshal(%+v): %v", minimal, err)
	}
	want = `{"version":"dev"}`
	if string(b) != want {
		t.Errorf("json.Marshal(minimal) = %s, want %s", b, want)
	}
}
