// Package version reports the running build's version.
//
// Release builds get Version, Commit and Date from linker flags (see
// .goreleaser.yaml). Builds made with `go install module@vX.Y.Z` carry the
// module version in Go's build info, and plain `go build` reports "dev".
package version

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
)

// Set by the linker for release builds: -X .../version.Version=0.1.0 and so on.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String returns a one-line description such as
// "jenktui v0.1.0 (commit abc1234, built 2026-11-16)".
// pseudoVersion matches the auto-generated versions Go gives untagged commits,
// such as v0.0.0-20261110201212-861463fa4ca9 (optionally with +dirty).
var pseudoVersion = regexp.MustCompile(`-\d{14}-[0-9a-f]{12}(\+dirty)?$`)

func String() string {
	info, _ := debug.ReadBuildInfo()
	return format(Version, Commit, Date, info)
}

func format(version, commit, date string, info *debug.BuildInfo) string {
	if version == "" || version == "dev" {
		// Not a release build: use the module version from `go install`, or
		// the VCS details Go records when built inside a git checkout.
		version = "dev"
		if info != nil {
			if v := info.Main.Version; v != "" && v != "(devel)" && !pseudoVersion.MatchString(v) {
				version = v
			}
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if commit == "" {
						commit = s.Value
					}
				case "vcs.time":
					if date == "" {
						date = s.Value
					}
				}
			}
		}
	}
	if version != "dev" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	out := "jenktui " + version
	var details []string
	if commit != "" {
		details = append(details, "commit "+short(commit))
	}
	if date != "" {
		details = append(details, "built "+day(date))
	}
	if len(details) > 0 {
		out += fmt.Sprintf(" (%s)", strings.Join(details, ", "))
	}
	return out
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// day keeps the date part of an RFC 3339 timestamp.
func day(s string) string {
	if i := strings.IndexByte(s, 'T'); i > 0 {
		return s[:i]
	}
	return s
}
