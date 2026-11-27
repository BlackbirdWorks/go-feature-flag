package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relayapp"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

func main() {
	// SIGTERM and SIGINT end the relay in order
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := relayapp.Main(
		ctx,
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		os.LookupEnv,
		relayapp.Build{Version: version, Commit: commit, Date: date},
	)

	stop()
	os.Exit(code)
}
