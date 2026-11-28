package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// the first signal ends the context; the default handling comes back, so that a second one ends the process
	go func() {
		<-ctx.Done()
		stop()
	}()

	code := cliapp.Main(
		ctx,
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		os.LookupEnv,
		cliapp.Build{Version: version, Commit: commit, Date: date},
	)

	stop()
	os.Exit(code)
}
