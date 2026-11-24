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
	fs := flag.NewFlagSet("soak", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.target, "target", "library", "library | relay")
	fs.StringVar(&o.endpoint, "endpoint", string(relay.SemaphoreEval),
		"relay endpoint: semaphore-eval | semaphore-allflags | ofrep-eval | ofrep-bulk")
	fs.StringVar(&o.protocol, "protocol", "http1",
		"relay client and server protocol: http1 (keep-alive) | h2c (HTTP/2 cleartext)")
	fs.StringVar(&o.exporter, "exporter", "discard", "discard | slow | failing")
	fs.DurationVar(&o.duration, "duration", 60*time.Second, "total run time")
	fs.DurationVar(&o.interval, "interval", 5*time.Second, "sampling interval")
	fs.IntVar(&o.workers, "workers", runtime.GOMAXPROCS(0), "number of load goroutines")
	fs.StringVar(&o.flagKey, "flag", "rule-bool-track", "flag key to evaluate (tracking on by default)")
	fs.Int64Var(&o.flushMs, "flush-ms", 100, "exporter flush interval (ms)")
	fs.Int64Var(
		&o.maxEvents,
		"max-events",
		10000,
		"exporter MaxEventInMemory (a small backlog keeps the heap measuring leaks, not the pending events)",
	)
	fs.IntVar(&o.warmup, "warmup", 0, "warm-up samples ignored by the verdict (default max(2, 20%))")
	fs.Float64Var(&o.heapTolerance, "heap-tolerance", 0.25, "relative HeapInuse growth tolerated")
	fs.StringVar(&o.cpuProfile, "cpuprofile", "", "write a CPU profile of the whole run to this file")

	return o, fs.Parse(args)
}

// newTarget builds the target selected by the options.
func newTarget(o options) (loadtest.Target, error) {
	mode, err := loadtest.ParseExporterMode(o.exporter)
	if err != nil {
		return nil, err
	}
	switch o.target {
	case "library":
		return loadtest.NewLibraryTarget(loadtest.LibraryOptions{
			FlagKey: o.flagKey, ExporterMode: mode,
			FlushInterval: time.Duration(o.flushMs) * time.Millisecond, MaxEventInMemory: o.maxEvents,
		})
	case "relay":
		ep, parseEndpointErr := relay.ParseEndpoint(o.endpoint)
		if parseEndpointErr != nil {
			return nil, parseEndpointErr
		}
		if o.protocol != "http1" && o.protocol != "h2c" {
			return nil, fmt.Errorf("%w %q (want http1 or h2c)", errUnknownProtocol, o.protocol)
		}

		return relay.NewTarget(relay.Options{
			FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: o.protocol == "h2c",
			FlushIntervalMs: o.flushMs, MaxEventInMemory: o.maxEvents,
		}, ep, o.flagKey, o.workers)
	default:
		return nil, fmt.Errorf("%w %q", errUnknownTarget, o.target)
	}
}

// startCPUProfile starts a CPU profile written to path and returns the function that stops it.
func startCPUProfile(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if startCPUProfileErr := pprof.StartCPUProfile(f); startCPUProfileErr != nil {
		_ = f.Close()

		return nil, startCPUProfileErr
	}

	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

// run is main with its inputs injected: it returns the exit status (0 PASS, 1 FAIL, 2 usage or setup
// error, 130 interrupted).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	slog.SetLogLoggerLevel(slog.LevelError)

	if o.cpuProfile != "" {
		stop, startCPUProfileErr := startCPUProfile(o.cpuProfile)
		if startCPUProfileErr != nil {
			_, _ = fmt.Fprintln(stderr, "error:", startCPUProfileErr)

			return 2
		}
		defer stop()
	}
	t, err := newTarget(o)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)

		return 2
	}
	defer t.Close()

	name := fmt.Sprintf("target=%s exporter=%s workers=%d", o.target, o.exporter, o.workers)
	if o.target == "relay" {
		name += " endpoint=" + o.endpoint + " protocol=" + o.protocol
	}
	_, _ = fmt.Fprintf(stdout, "# %s GOMAXPROCS=%d %s duration=%s\n",
		name, runtime.GOMAXPROCS(0), runtime.Version(), o.duration)
	rep := loadtest.Run(ctx, t, loadtest.Options{
		Name: name, Duration: o.duration, Interval: o.interval, Workers: o.workers,
		WarmupIntervals: o.warmup, HeapTolerance: o.heapTolerance,
	}, stdout)
	if ctx.Err() != nil {
		_, _ = fmt.Fprintln(stderr, "interrupted: no verdict")

		return 130
	}
	rep.Summary(stdout)
	if !rep.Pass {
		return 1
	}

	return 0
}
