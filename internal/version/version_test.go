package version

import (
	"runtime/debug"
	"testing"
)

func TestFormat(t *testing.T) {
	info := func(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}, Settings: settings}
	}
	tests := []struct {
		name                  string
		version, commit, date string
		info                  *debug.BuildInfo
		want                  string
	}{
		{"release build from linker flags", "0.1.0", "abcdef1234567", "2026-11-16T10:00:00Z", nil,
			"jenktui v0.1.0 (commit abcdef1, built 2026-11-16)"},
		{"linker version already has v", "v0.1.0", "", "", nil, "jenktui v0.1.0"},
		{"plain go build, no info", "dev", "", "", nil, "jenktui dev"},
		{"empty version means dev", "", "", "", nil, "jenktui dev"},
		{"go install at a tag", "dev", "", "", info("v0.2.0"), "jenktui v0.2.0"},
		{"untagged commit gets a pseudo-version, shown as dev", "dev", "", "", info("v0.0.0-20261110201212-861463fa4ca9+dirty"), "jenktui dev"},
		{"go build in a git checkout", "dev", "", "", info("(devel)",
			debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"},
			debug.BuildSetting{Key: "vcs.time", Value: "2026-11-10T08:30:00Z"}),
			"jenktui dev (commit 0123456, built 2026-11-10)"},
		{"linker values win over build info", "0.1.0", "aaaaaaa", "", info("(devel)",
			debug.BuildSetting{Key: "vcs.revision", Value: "bbbbbbbbbb"}),
			"jenktui v0.1.0 (commit aaaaaaa)"},
	}
	for _, tc := range tests {
		if got := format(tc.version, tc.commit, tc.date, tc.info); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
