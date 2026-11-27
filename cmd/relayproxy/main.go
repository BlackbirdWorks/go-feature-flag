package main

import (
	"context"
	"crypto/fips140"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

// versionText is what --version prints.
func versionText() string {
	return buildinfo.Text("semaphore", buildinfo.Linked{Version: version, Commit: commit, Date: date},
		buildinfo.Read(), fips140.Enabled())
}

// newConfigFlagSet declares the command line flags. config is declared so that its value is
// consumed as a value even when it starts with a dash; unknown flags (--version, -v) are skipped
// instead of stopping the parse and losing --config.
func newConfigFlagSet() *pflag.FlagSet {
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.String("config", "", "Location of the version 2 bootstrap file (or SEMAPHORE_CONFIG)")
	f.ParseErrorsAllowlist.UnknownFlags = true

	return f
}

// versionRequested reports whether args ask for --version / -v. It uses a flag set of its
// own so that the flag never reaches the configuration loader.
func versionRequested(args []string) bool {
	f := newConfigFlagSet()
	f.SetOutput(io.Discard)
	show := f.BoolP("version", "v", false, "Print the version and exit")
	_ = f.Parse(args)

	return *show
}

const banner = `█▀▀ █▀█   █▀▀ █▀▀ ▄▀█ ▀█▀ █ █ █▀█ █▀▀   █▀▀ █   ▄▀█ █▀▀
█▄█ █▄█   █▀  ██▄ █▀█  █  █▄█ █▀▄ ██▄   █▀  █▄▄ █▀█ █▄█

     █▀█ █▀▀ █   ▄▀█ █▄█   █▀█ █▀█ █▀█ ▀▄▀ █▄█
     █▀▄ ██▄ █▄▄ █▀█  █    █▀▀ █▀▄ █▄█ █ █  █ 

Semaphore Relay Proxy - Version %s
_____________________________________________`

func main() {
	os.Exit(runProcess())
}

// runProcess runs the relay until a signal ends it and returns the exit code.
func runProcess() int {
	// SIGTERM and SIGINT end the process in order, SIGHUP reloads the bootstrap file.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)

	reload := make(chan struct{}, 1)

	go forwardSignals(ctx, hup, reload)

	return run(ctx, &runOptions{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Load:   osLoadOptions(),
		Reload: reload,
	})
}

// forwardSignals turns every signal of in into a reload request: requests that arrive while one waits are merged.
func forwardSignals(ctx context.Context, in <-chan os.Signal, out chan<- struct{}) {
	for {
		select {
		case <-in:
			select {
			case out <- struct{}{}:
			default:
			}
		case <-ctx.Done():
			return
		}
	}
}
