package main

import (
	"context"
	"crypto/fips140"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
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

// declareFlags declares the command line flags of the relay.
func declareFlags(flags *pflag.FlagSet) {
	flags.String("config", "", "Location of the version 2 bootstrap file (or SEMAPHORE_CONFIG)")
	flags.String("log-level", "", "observability.logging.level (or SEMAPHORE_LOG_LEVEL)")
	flags.String("log-format", "", "observability.logging.format (or SEMAPHORE_LOG_FORMAT)")
	flags.String("listen", "", "Address of the api listener (or SEMAPHORE_LISTEN)")
	flags.String("ops-listen", "", "Address of the ops listener (or SEMAPHORE_OPS_LISTEN)")
	flags.String("manifest-key", "", "controlPlane.manifest.key (or SEMAPHORE_MANIFEST_KEY)")
	flags.String("state-dir", "", "controlPlane.stateDir, empty for no state directory (or SEMAPHORE_STATE_DIR)")
}

// newConfigFlagSet declares the command line flags. config is declared so that its value is
// consumed as a value even when it starts with a dash; unknown flags (--version, -v) are skipped
// instead of stopping the parse and losing --config. It is the flag set of the version check only: the relay parses
// with [newStrictFlagSet].
func newConfigFlagSet() *pflag.FlagSet {
	flags := pflag.NewFlagSet("config", pflag.ContinueOnError)
	declareFlags(flags)
	flags.ParseErrorsAllowlist.UnknownFlags = true

	return flags
}

// newStrictFlagSet is the flag set of the relay: an unknown flag is an error, and --version and --help are declared.
func newStrictFlagSet() *pflag.FlagSet {
	flags := pflag.NewFlagSet("semaphore", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	declareFlags(flags)
	flags.BoolP("version", "v", false, "Print the version and exit")
	// declared, so that the parse reads the whole command line (an unknown flag after --help is an error) before
	// the help is answered
	flags.BoolP("help", "h", false, "Print this help and exit")

	return flags
}

// usageText is what --help prints: every flag with its environment variable.
func usageText() string {
	return "Usage: semaphore --config FILE [flags]\n\n" + newStrictFlagSet().FlagUsages()
}

// errUsage is wrapped by the error of command line flags that do not parse.
var errUsage = errors.New("invalid command line")

// overridesFrom reads the flags that override the file (a flag wins over the environment, which wins over the file).
// --help is [pflag.ErrHelp].
func overridesFrom(args []string) (*bootstrap.Overrides, error) {
	flags := newStrictFlagSet()

	err := flags.Parse(args)
	if errors.Is(err, pflag.ErrHelp) {
		return nil, err //nolint:wrapcheck // the caller matches the sentinel of the flag package
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	if help, _ := flags.GetBool("help"); help {
		return nil, pflag.ErrHelp
	}

	get := func(name string) string {
		value, _ := flags.GetString(name)

		return value
	}

	return &bootstrap.Overrides{
		Config: get("config"), LogLevel: get("log-level"), LogFormat: get("log-format"), Listen: get("listen"),
		OpsListen: get("ops-listen"), ManifestKey: get("manifest-key"), StateDir: get("state-dir"),
		StateDirGiven: flags.Changed("state-dir"),
	}, nil
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
