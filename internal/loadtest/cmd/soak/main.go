// Command soak runs a max-rate load against the library or a real relay proxy (loopback HTTP),
// with event tracking on and a selectable exporter behaviour, and prints a time series and a
// "memory stable" verdict. It exits with status 1 when the verdict is FAIL.
//
//	go run ./internal/loadtest/cmd/soak -target library -exporter slow -duration 60s
//	go run ./internal/loadtest/cmd/soak -target relay -endpoint ofrep-eval -exporter failing
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/thomaspoignant/go-feature-flag/internal/loadtest"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest/relay"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		target   = flag.String("target", "library", "library | relay")
		endpoint = flag.String("endpoint", string(relay.GOFFEval),
			"relay endpoint: goff-eval | goff-allflags | ofrep-eval | ofrep-bulk")
		exp      = flag.String("exporter", "discard", "discard | slow | failing")
		duration = flag.Duration("duration", 60*time.Second, "total run time")
		interval = flag.Duration("interval", 5*time.Second, "sampling interval")
		workers  = flag.Int("workers", runtime.GOMAXPROCS(0), "number of load goroutines")
		flagKey  = flag.String("flag", "rule-bool-track", "flag key to evaluate (tracking on by default)")
		flushMs  = flag.Int64("flush-ms", 1000, "exporter flush interval (ms)")
		maxMem   = flag.Int64("max-events", 100000, "exporter MaxEventInMemory")
		warmup   = flag.Int("warmup", 0, "warm-up samples ignored by the verdict (default max(2, 20%))")
		tol      = flag.Float64("heap-tolerance", 0.25, "relative HeapInuse growth tolerated")
		cpuProf  = flag.String("cpuprofile", "", "write a CPU profile of the whole run to this file")
	)
	flag.Parse()
	slog.SetLogLoggerLevel(slog.LevelError)

	if *cpuProf != "" {
		f, err := os.Create(*cpuProf)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		defer pprof.StopCPUProfile()
	}

	mode, err := loadtest.ParseExporterMode(*exp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
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
		t, err = relay.NewTarget(relay.Options{
			FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode,
			FlushIntervalMs: *flushMs, MaxEventInMemory: *maxMem,
		}, ep, *flagKey, *workers)
	default:
		err = fmt.Errorf("unknown target %q", *target)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	defer t.Close()

	name := fmt.Sprintf("target=%s exporter=%s workers=%d", *target, *exp, *workers)
	if *target == "relay" {
		name += " endpoint=" + *endpoint
	}
	fmt.Printf("# %s GOMAXPROCS=%d %s duration=%s\n", name, runtime.GOMAXPROCS(0), runtime.Version(), *duration)
	// Ctrl+C / SIGTERM stop the workers (the relay proxy also shuts itself down on them).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rep := loadtest.Run(ctx, t, loadtest.Options{
		Name: name, Duration: *duration, Interval: *interval, Workers: *workers,
		WarmupIntervals: *warmup, HeapTolerance: *tol,
	}, os.Stdout)
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "interrupted: no verdict")
		return 130
	}
	rep.Summary(os.Stdout)
	if !rep.Pass {
		return 1
	}
	return 0
}
