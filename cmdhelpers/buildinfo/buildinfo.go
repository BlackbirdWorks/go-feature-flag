// Package buildinfo formats the identity of a Semaphore binary for the --version flags.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Linked holds the values a release build injects with -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// An empty field means "not injected".
type Linked struct {
	Version string
	Commit  string
	Date    string
}

// DevVersion is the version of a build that was not given one (the default of main.version).
const DevVersion = "localdev"

const unknown = "unknown"

// Text returns the version output: one "key: value" line each for the name, version, commit,
// build date, Go version and FIPS 140 mode. Values missing from the link flags come from the
// module build information (go install, go build in a git checkout) and are "unknown" otherwise.
// bi may be nil.
func Text(name string, linked Linked, bi *debug.BuildInfo, fips bool) string {
	version, commit, date := linked.Version, linked.Commit, linked.Date
	goVersion := runtime.Version()
	if bi != nil {
		goVersion = bi.GoVersion
		if (version == "" || version == DevVersion) && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && commit == "":
				commit = s.Value
			case s.Key == "vcs.time" && date == "":
				date = s.Value
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", name, orUnknown(version))
	fmt.Fprintf(&b, "commit: %s\n", orUnknown(commit))
	fmt.Fprintf(&b, "built: %s\n", orUnknown(date))
	fmt.Fprintf(&b, "go: %s\n", goVersion)
	fmt.Fprintf(&b, "fips140: %t\n", fips)

	return b.String()
}

// Read returns the module build information of the running binary, or nil when there is none.
func Read() *debug.BuildInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	return bi
}

func orUnknown(s string) string {
	if s == "" {
		return unknown
	}

	return s
}
