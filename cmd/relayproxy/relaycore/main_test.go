package relaycore_test

import (
	"os"
	"testing"
)

// TestMain pins the clock, and with the docker tag starts the shared emulator (see run_test.go and
// run_docker_test.go).
func TestMain(m *testing.M) {
	os.Exit(run(m))
}
