package soakapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

const (
	// Exit codes of the command, besides 0 and 1.
	exitUsage       = 2
	exitInterrupted = 130
)

var (
	errUnknownProtocol = errors.New("unknown protocol")
	errUnknownTarget   = errors.New("unknown target")
)

// Main parses args and runs the soak harness until ctx is cancelled, returning the process exit code
// (0 PASS, 1 FAIL, 2 usage or setup error, 130 interrupted).
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, loadtest.Run)
}

// soakRunner executes the soak loop against target and returns its report.
// Production calls pass [loadtest.Run]; tests inject a deterministic sampler.
type soakRunner func(
	ctx context.Context,
	target loadtest.Target,
	opts *loadtest.Options,
	out io.Writer,
) loadtest.Report

// soakTarget wraps [loadtest.Target] so newTarget returns a concrete type.
type soakTarget struct {
	loadtest.Target
}

// FlagsPerCall forwards [loadtest.Weighted] when the wrapped target implements it.
func (s soakTarget) FlagsPerCall() int {
	if weighted, ok := s.Target.(loadtest.Weighted); ok {
		return weighted.FlagsPerCall()
	}

	return 1
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, execute soakRunner) int {
	opts, err := parseCommandLine(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "soak: %v\n\n%s", err, usageText())

		return exitUsage
	}

	if opts.Help {
		_, _ = fmt.Fprint(stdout, usageText())

		return 0
	}

	slog.SetLogLoggerLevel(slog.LevelError)

	if opts.CPUProfile != "" {
		stop, startCPUProfileErr := startCPUProfile(opts.CPUProfile)
		if startCPUProfileErr != nil {
			_, _ = fmt.Fprintln(stderr, "error:", startCPUProfileErr)

			return exitUsage
		}
		defer stop()
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	target, err := newTarget(ctx, &opts, workers)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)

		return exitUsage
	}
	defer target.Close()

	name := fmt.Sprintf("target=%s exporter=%s workers=%d", opts.Target, opts.Exporter, workers)
	if opts.Target == "relay" {
		name += " endpoint=" + opts.Endpoint + " protocol=" + opts.Protocol
	}

	_, _ = fmt.Fprintf(stdout, "# %s GOMAXPROCS=%d %s duration=%s\n",
		name, runtime.GOMAXPROCS(0), runtime.Version(), opts.Duration)

	rep := execute(ctx, target, &loadtest.Options{
		Name: name, Duration: opts.Duration, Interval: opts.Interval, Workers: workers,
		WarmupIntervals: opts.Warmup, HeapTolerance: opts.HeapTolerance,
	}, stdout)
	if ctx.Err() != nil {
		_, _ = fmt.Fprintln(stderr, "interrupted: no verdict")

		return exitInterrupted
	}

	rep.Summary(stdout)

	if !rep.Pass {
		return 1
	}

	return 0
}

// newTarget builds the target selected by the options.
func newTarget(ctx context.Context, opts *cli, workers int) (soakTarget, error) {
	mode, err := loadtest.ParseExporterMode(opts.Exporter)
	if err != nil {
		return soakTarget{}, fmt.Errorf("parse exporter mode: %w", err)
	}

	switch opts.Target {
	case "library":
		libTarget, libErr := loadtest.NewLibraryTarget(ctx, loadtest.LibraryOptions{
			FlagKey: opts.FlagKey, ExporterMode: mode,
			FlushInterval: time.Duration(opts.FlushMs) * time.Millisecond, MaxEventInMemory: opts.MaxEvents,
		})
		if libErr != nil {
			return soakTarget{}, fmt.Errorf("create library soak target: %w", libErr)
		}

		return soakTarget{Target: libTarget}, nil
	case "relay":
		endpoint, parseEndpointErr := relay.ParseEndpoint(opts.Endpoint)
		if parseEndpointErr != nil {
			return soakTarget{}, fmt.Errorf("parse relay endpoint: %w", parseEndpointErr)
		}

		if opts.Protocol != "http1" && opts.Protocol != "h2c" {
			return soakTarget{}, fmt.Errorf("%w %q (want http1 or h2c)", errUnknownProtocol, opts.Protocol)
		}

		//nolint:contextcheck // the relay of the target owns its loopback server, Target.Close stops it (ctx does not)
		relayTarget, relayErr := relay.NewTarget(relay.Options{
			FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: opts.Protocol == "h2c",
			FlushIntervalMs: opts.FlushMs, MaxEventInMemory: opts.MaxEvents,
		}, endpoint, opts.FlagKey, workers)
		if relayErr != nil {
			return soakTarget{}, fmt.Errorf("create relay soak target: %w", relayErr)
		}

		return soakTarget{Target: relayTarget}, nil
	default:
		return soakTarget{}, fmt.Errorf("%w %q", errUnknownTarget, opts.Target)
	}
}

// startCPUProfile starts a CPU profile written to path and returns the function that stops it.
func startCPUProfile(path string) (func(), error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile file: %w", err)
	}

	startCPUProfileErr := pprof.StartCPUProfile(file)
	if startCPUProfileErr != nil {
		_ = file.Close()

		return nil, fmt.Errorf("start CPU profile: %w", startCPUProfileErr)
	}

	return func() {
		pprof.StopCPUProfile()

		_ = file.Close()
	}, nil
}
