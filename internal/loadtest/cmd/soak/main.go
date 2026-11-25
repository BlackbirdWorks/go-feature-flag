// Command soak runs a max-rate load against the library or a real relay proxy (loopback HTTP),
// with event tracking on and a selectable exporter behaviour, and prints a time series and a
// "memory stable" verdict. It exits with status 1 when the verdict is FAIL.
//
//	go run ./internal/loadtest/cmd/soak -target library -exporter slow -duration 60s
//	go run ./internal/loadtest/cmd/soak -target relay -endpoint ofrep-eval -exporter failing
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

const (
	// Defaults of the flags of the command.
	defaultDuration      = 60 * time.Second
	defaultInterval      = 5 * time.Second
	defaultFlushMillis   = 100
	defaultMaxEvents     = 10000
	defaultHeapTolerance = 0.25
	// Exit codes of the command, besides 0 and 1.
	exitUsage       = 2
	exitInterrupted = 130
)

var (
	errUnknownProtocol = errors.New("unknown protocol")
	errUnknownTarget   = errors.New("unknown target")
)

func main() {
	// Ctrl+C / SIGTERM stop the workers (the relay proxy also shuts itself down on them).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// options are the command line options.
type options struct {
	target, endpoint, protocol, exporter, flagKey, cpuProfile string
	duration, interval                                        time.Duration
	workers, warmup                                           int
	flushMs, maxEvents                                        int64
	heapTolerance                                             float64
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	flagSet := flag.NewFlagSet("soak", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	var opts options
	flagSet.StringVar(&opts.target, "target", "library", "library | relay")
	flagSet.StringVar(&opts.endpoint, "endpoint", string(relay.SemaphoreEval),
		"relay endpoint: semaphore-eval | semaphore-allflags | ofrep-eval | ofrep-bulk")
	flagSet.StringVar(&opts.protocol, "protocol", "http1",
		"relay client and server protocol: http1 (keep-alive) | h2c (HTTP/2 cleartext)")
	flagSet.StringVar(&opts.exporter, "exporter", "discard", "discard | slow | failing")
	flagSet.DurationVar(&opts.duration, "duration", defaultDuration, "total run time")
	flagSet.DurationVar(&opts.interval, "interval", defaultInterval, "sampling interval")
	flagSet.IntVar(&opts.workers, "workers", runtime.GOMAXPROCS(0), "number of load goroutines")
	flagSet.StringVar(&opts.flagKey, "flag", "rule-bool-track", "flag key to evaluate (tracking on by default)")
	flagSet.Int64Var(&opts.flushMs, "flush-ms", defaultFlushMillis, "exporter flush interval (ms)")
	flagSet.Int64Var(
		&opts.maxEvents,
		"max-events",
		defaultMaxEvents,
		"exporter MaxEventInMemory (a small backlog keeps the heap measuring leaks, not the pending events)",
	)
	flagSet.IntVar(&opts.warmup, "warmup", 0, "warm-up samples ignored by the verdict (default max(2, 20%))")
	flagSet.Float64Var(
		&opts.heapTolerance,
		"heap-tolerance",
		defaultHeapTolerance,
		"relative HeapInuse growth tolerated",
	)
	flagSet.StringVar(&opts.cpuProfile, "cpuprofile", "", "write a CPU profile of the whole run to this file")

	err := flagSet.Parse(args)
	if err != nil {
		return options{}, fmt.Errorf("parse soak flags: %w", err)
	}

	return opts, nil
}

// newTarget builds the target selected by the options.
func newTarget(ctx context.Context, opts options) (loadtest.Target, error) {
	mode, err := loadtest.ParseExporterMode(opts.exporter)
	if err != nil {
		return nil, fmt.Errorf("parse exporter mode: %w", err)
	}
	switch opts.target {
	case "library":
		t, libErr := loadtest.NewLibraryTarget(ctx, loadtest.LibraryOptions{
			FlagKey: opts.flagKey, ExporterMode: mode,
			FlushInterval: time.Duration(opts.flushMs) * time.Millisecond, MaxEventInMemory: opts.maxEvents,
		})
		if libErr != nil {
			return nil, fmt.Errorf("create library soak target: %w", libErr)
		}

		return t, nil
	case "relay":
		endpoint, parseEndpointErr := relay.ParseEndpoint(opts.endpoint)
		if parseEndpointErr != nil {
			return nil, fmt.Errorf("parse relay endpoint: %w", parseEndpointErr)
		}
		if opts.protocol != "http1" && opts.protocol != "h2c" {
			return nil, fmt.Errorf("%w %q (want http1 or h2c)", errUnknownProtocol, opts.protocol)
		}

		//nolint:contextcheck // relay.Target owns its own loopback server lifecycle stopped by Target.Close
		t, relayErr := relay.NewTarget(relay.Options{
			FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: opts.protocol == "h2c",
			FlushIntervalMs: opts.flushMs, MaxEventInMemory: opts.maxEvents,
		}, endpoint, opts.flagKey, opts.workers)
		if relayErr != nil {
			return nil, fmt.Errorf("create relay soak target: %w", relayErr)
		}

		return t, nil
	default:
		return nil, fmt.Errorf("%w %q", errUnknownTarget, opts.target)
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

// run is main with its inputs injected: it returns the exit status (0 PASS, 1 FAIL, 2 usage or setup
// error, 130 interrupted).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return exitUsage
	}
	slog.SetLogLoggerLevel(slog.LevelError)

	if opts.cpuProfile != "" {
		stop, startCPUProfileErr := startCPUProfile(opts.cpuProfile)
		if startCPUProfileErr != nil {
			_, _ = fmt.Fprintln(stderr, "error:", startCPUProfileErr)

			return exitUsage
		}
		defer stop()
	}
	t, err := newTarget(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)

		return exitUsage
	}
	defer t.Close()

	name := fmt.Sprintf("target=%s exporter=%s workers=%d", opts.target, opts.exporter, opts.workers)
	if opts.target == "relay" {
		name += " endpoint=" + opts.endpoint + " protocol=" + opts.protocol
	}
	_, _ = fmt.Fprintf(stdout, "# %s GOMAXPROCS=%d %s duration=%s\n",
		name, runtime.GOMAXPROCS(0), runtime.Version(), opts.duration)
	rep := loadtest.Run(ctx, t, loadtest.Options{
		Name: name, Duration: opts.duration, Interval: opts.interval, Workers: opts.workers,
		WarmupIntervals: opts.warmup, HeapTolerance: opts.heapTolerance,
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
