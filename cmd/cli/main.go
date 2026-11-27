package main

import (
	"context"
	"os"

	"github.com/blackbirdworks/semaphore/cmd/cli/cliapp"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

func main() {
	code := cliapp.Main(
		context.Background(),
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		os.LookupEnv,
		cliapp.Build{Version: version, Commit: commit, Date: date},
	)
	os.Exit(code)
}
