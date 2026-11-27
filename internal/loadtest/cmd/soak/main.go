// Command soak runs a max-rate load against the library or a real relay proxy (loopback HTTP),
// with event tracking on and a selectable exporter behaviour, and prints a time series and a
// "memory stable" verdict. It exits with status 1 when the verdict is FAIL.
//
//	go run ./internal/loadtest/cmd/soak --target library --exporter slow --duration 60s
//	go run ./internal/loadtest/cmd/soak --target relay --endpoint ofrep-eval --exporter failing
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/blackbirdworks/semaphore/internal/loadtest/cmd/soak/soakapp"
)

func main() {
	// Ctrl+C / SIGTERM stop the workers (the relay proxy also shuts itself down on them).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := soakapp.Main(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()
	os.Exit(code)
}
