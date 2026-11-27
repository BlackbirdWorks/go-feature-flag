package helper

import "io"

// Streams holds the output writers injected into CLI commands.
type Streams struct {
	Stdout io.Writer
	Stderr io.Writer
}
