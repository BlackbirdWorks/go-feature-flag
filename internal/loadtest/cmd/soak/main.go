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

func main() {
	// Ctrl+C / SIGTERM stop the workers (the relay proxy also shuts itself down on them).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main with its inputs injected: it returns the exit status (0 PASS, 1 FAIL, 2 usage or setup
// error, 130 interrupted).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("soak", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		target   = fs.String("target", "library", "library | relay")
		endpoint = fs.String("endpoint", string(relay.SemaphoreEval),
			"relay endpoint: semaphore-eval | semaphore-allflags | ofrep-eval | ofrep-bulk")
		protocol = fs.String("protocol", "http1", "relay client and server protocol: http1 (keep-alive) | h2c (HTTP/2 cleartext)")
		exp      = fs.String("exporter", "discard", "discard | slow | failing")
		duration = fs.Duration("duration", 60*time.Second, "total run time")
		interval = fs.Duration("interval", 5*time.Second, "sampling interval")
		workers  = fs.Int("workers", runtime.GOMAXPROCS(0), "number of load goroutines")
		flagKey  = fs.String("flag", "rule-bool-track", "flag key to evaluate (tracking on by default)")
		flushMs  = fs.Int64("flush-ms", 1000, "exporter flush interval (ms)")
		maxMem   = fs.Int64("max-events", 100000, "exporter MaxEventInMemory")
		warmup   = fs.Int("warmup", 0, "warm-up samples ignored by the verdict (default max(2, 20%))")
		tol      = fs.Float64("heap-tolerance", 0.25, "relative HeapInuse growth tolerated")
		cpuProf  = fs.String("cpuprofile", "", "write a CPU profile of the whole run to this file")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	slog.SetLogLoggerLevel(slog.LevelError)

	if *cpuProf != "" {
		f, err := os.Create(*cpuProf)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		defer pprof.StopCPUProfile()
	}

	mode, err := loadtest.ParseExporterMode(*exp)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	var t loadtest.Target
	switch *target {
	case "library":
		t, err = loadtest.NewLibraryTarget(loadtest.LibraryOptions{
			FlagKey: *flagKey, ExporterMode: mode,
			FlushInterval: time.Duration(*flushMs) * time.Millisecond, MaxEventInMemory: *maxMem,
		})
	case "relay":
		var ep relay.Endpoint
		if ep, err = relay.ParseEndpoint(*endpoint); err != nil {
			break
		}
		if *protocol != "http1" && *protocol != "h2c" {
			err = fmt.Errorf("unknown protocol %q (want http1 or h2c)", *protocol)
			break
		}
		t, err = relay.NewTarget(relay.Options{
			FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: *protocol == "h2c",
			FlushIntervalMs: *flushMs, MaxEventInMemory: *maxMem,
		}, ep, *flagKey, *workers)
	default:
		err = fmt.Errorf("unknown target %q", *target)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	defer t.Close()

	name := fmt.Sprintf("target=%s exporter=%s workers=%d", *target, *exp, *workers)
	if *target == "relay" {
		name += " endpoint=" + *endpoint + " protocol=" + *protocol
	}
	fmt.Fprintf(stdout, "# %s GOMAXPROCS=%d %s duration=%s\n", name, runtime.GOMAXPROCS(0), runtime.Version(), *duration)
	rep := loadtest.Run(ctx, t, loadtest.Options{
		Name: name, Duration: *duration, Interval: *interval, Workers: *workers,
		WarmupIntervals: *warmup, HeapTolerance: *tol,
	}, stdout)
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "interrupted: no verdict")
		return 130
	}
	rep.Summary(stdout)
	if !rep.Pass {
		return 1
	}
	return 0
}
